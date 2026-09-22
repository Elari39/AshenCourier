package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"

	"ashen-courier/internal/domain"
	"uuid"
)

// TestStorageError 是 F1 的回归网：PG 不可用时必须能被上层识别成
// 「依赖不可用」（→ 503 + Retry-After），而不是落进 500 的默认分支。
func TestStorageError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		err             error
		wantUnavailable bool
	}{
		{"没有错误", nil, false},
		{"查无此行是正常分支", pgx.ErrNoRows, false},
		{"唯一约束冲突是业务错误", &pgconn.PgError{Code: uniqueViolation}, false},
		{"语法错误是业务错误", &pgconn.PgError{Code: "42601"}, false},
		{"被 %w 包住的约束冲突仍是业务错误", fmt.Errorf("store: %w", &pgconn.PgError{Code: checkViolation}), false},
		{"拨号失败是依赖不可用", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"连接池已关闭是依赖不可用", puddle.ErrClosedPool, true},
		{"操作超时是依赖不可用", context.DeadlineExceeded, true},
		{"上游取消也算可重试", context.Canceled, true},

		// ---- 带 SQLSTATE 的「数据库暂时不可用」（审计 B3/B6）----
		// 这四条过去会被 pgErrCode != "" 一律当作「库健康」→ 500，于是过载或
		// PG 重启的窗口里客户端拿到 500、不会退避，反而加重拥堵 —— 恰恰发生在
		// 最需要退避的时刻。现在它们必须与「拨号失败」同一档（→ 503 + Retry-After）。
		{"连接失败类（08006）是依赖不可用", &pgconn.PgError{Code: "08006"}, true},
		{"连接不存在（08003）是依赖不可用", &pgconn.PgError{Code: "08003"}, true},
		{"连接数打满（53300）是依赖不可用", &pgconn.PgError{Code: "53300"}, true},
		{"服务端正在关停（57P01）是依赖不可用", &pgconn.PgError{Code: "57P01"}, true},
		{"服务端正在启动（57P03）是依赖不可用", &pgconn.PgError{Code: "57P03"}, true},
		{"被 %w 包住的连接失败仍是依赖不可用", fmt.Errorf("store: %w", &pgconn.PgError{Code: "08006"}), true},

		// ---- 负对照：带 SQLSTATE 但「库是健康的」，不能顺手也归成 503 ----
		// 少了这几条，一个「凡带 SQLSTATE 就 503」的粗暴改法会是绿的 ——
		// 而那会把每次用户输错邮箱（23505）都变成 503，上游开始无谓退避。
		{"数据异常（22012 除零）仍是业务错误", &pgconn.PgError{Code: "22012"}, false},
		{"约束名不存在（42P01）仍是业务错误", &pgconn.PgError{Code: "42P01"}, false},
		{"序列化失败不是依赖不可用（库健康，该由应用层重试整个请求）", &pgconn.PgError{Code: "40001"}, false},
		{"死锁不是依赖不可用（同上）", &pgconn.PgError{Code: "40P01"}, false},
		{"查询被取消（57014）不是依赖不可用（类 57 里只认 57P0x）", &pgconn.PgError{Code: "57014"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := storageError(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("storageError(nil) = %v, want nil", got)
				}
				return
			}
			if errors.Is(got, domain.ErrUnavailable) != tc.wantUnavailable {
				t.Fatalf("storageError(%v) = %v：errors.Is(ErrUnavailable) = %v, want %v",
					tc.err, got, !tc.wantUnavailable, tc.wantUnavailable)
			}
			// 归类不能吃掉根因，否则日志与排障都失去线索
			if !errors.Is(got, tc.err) {
				t.Fatalf("归类后仍应能 errors.Is 到原始错误：got %v, want 匹配 %v", got, tc.err)
			}
		})
	}
}

// TestPgUnavailableCode 把「哪些 SQLSTATE 算数据库暂时不可用」这张表本身钉住。
//
// 与 TestStorageError 的分工：那一条测的是「storageError 有没有按这张表行事」，
// 这一条测的是「表的内容对不对」—— 两者都要有，否则改表与改调用会互相掩护。
// 尤其是**类前缀**的判定：只该按两字符类匹配，不能因为码里含 "08"/"53" 子串
// 就命中（例如 "08006" 命中类 08 是对的，但把匹配写成 strings.Contains 会让
// 任何含这两个字符的码都中招），也不能被顺手扩成整类 57。
func TestPgUnavailableCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want bool
	}{
		// 类 08 connection_exception
		{"08000", true}, {"08001", true}, {"08003", true}, {"08004", true},
		{"08006", true}, {"08007", true}, {"08P01", true},
		// 类 53 insufficient_resources
		{"53000", true}, {"53100", true}, {"53200", true},
		{"53400", true}, // 53300 单列在 TestStorageError 里，这里补类内其它码
		// 服务端关停 / 启动
		{"57P02", true}, {"57P03", true},
		// 边界：类 57 只认那三个码，整类纳入是错的
		{"57P01", true},
		{"57014", false}, {"57000", false}, {"57P04", false},
		// 其它类一律不动
		{"", false},
		{"23505", false}, {"23514", false}, {"42601", false}, {"42P01", false},
		{"22012", false}, {"40001", false}, {"40P01", false},
		{"22008", false}, // datetime_field_overflow：含 "08" 但类不是 08，
		//                   子串匹配式的实现（strings.Contains）会在这里误伤
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			if got := pgUnavailableCode(tc.code); got != tc.want {
				t.Fatalf("pgUnavailableCode(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestMapWriteError 确认写入类错误的三种归类：冲突 / 约束 / 依赖不可用。
func TestMapWriteError(t *testing.T) {
	t.Parallel()

	t.Run("唯一约束冲突", func(t *testing.T) {
		t.Parallel()

		err := mapWriteError(&pgconn.PgError{Code: uniqueViolation}, "email", "a@b.com")
		conflict, ok := domain.AsConflict(err)
		if !ok {
			t.Fatalf("期望 *ConflictError，实际 %v", err)
		}
		if conflict.Field != "email" {
			t.Fatalf("冲突字段 = %q, want email", conflict.Field)
		}
	})

	t.Run("CHECK 约束冲突是字段级校验错误", func(t *testing.T) {
		t.Parallel()

		err := mapWriteError(&pgconn.PgError{Code: checkViolation}, "target_url", "")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
	})

	t.Run("连接失败是依赖不可用", func(t *testing.T) {
		t.Parallel()

		err := mapWriteError(&net.OpError{Op: "read", Err: errors.New("connection reset")}, "short_code", "abc1234")
		if !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("期望 ErrUnavailable，实际 %v", err)
		}
	})
}

// TestStorageErrorOnRealDriverFailure 用真实 pgx 去连一个必然连不上的地址。
//
// 上面的表驱动用例只能证明「我们自己造的错」被判对；这条才能证明判定规则
// 匹配驱动的真实错误形状（pgx 的拨号失败是 *pgconn.ConnectError → *net.OpError
// → *os.SyscallError，链路上没有 SQLSTATE）。端口 1 上不会有 PostgreSQL 监听，
// connect_timeout 兜住最坏情况。
func TestStorageErrorOnRealDriverFailure(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// idle 连接由后台 goroutine 异步建立，因此构造连接池本身不会失败
	pool, err := pgxpool.New(ctx, "postgres://ashen:ashen@127.0.0.1:1/ashen?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatalf("构造连接池不该失败：%v", err)
	}
	defer pool.Close()

	store := &LinkStore{db: &DB{pool: pool, timeout: 3 * time.Second}}

	if _, err := store.GetByCode(ctx, "abc1234"); err == nil {
		t.Fatal("连不上数据库时必须返回错误")
	} else if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("读取路径：真实驱动失败应归类为 ErrUnavailable（→503），实际：%v", err)
	}

	writeErr := store.Create(ctx, &domain.Link{
		ID:        uuid.NewV7(),
		ShortCode: "abc1234",
		TargetURL: "https://example.com",
		Status:    domain.LinkStatusActive,
	})
	if writeErr == nil {
		t.Fatal("连不上数据库时必须返回错误")
	}
	if !errors.Is(writeErr, domain.ErrUnavailable) {
		t.Fatalf("写入路径：真实驱动失败应归类为 ErrUnavailable（→503），实际：%v", writeErr)
	}
}

// TestOpCtxTimesOutWithCause 守住 F3：每次 PG 调用都必须带 deadline，
// 且超时的取消原因要能被日志归因成「库慢」而不是「上游取消」。
func TestOpCtxTimesOutWithCause(t *testing.T) {
	t.Parallel()

	// timeout 不能取 1ms：断言若写成「读 deadline 时剩余时间必须 > 0」，
	// 那测的就不再是 opCtx，而是「从 opCtx 返回到读 deadline 之间调度不超过 1ms」。
	// 4 核 runner 上 -race 全量并行跑时真的会踩中 —— CI 上实测 -321µs；
	// 本机把 16 核压满后 2000 次里红 7 次，最差 -6ms。
	// 取 20ms 留出余量，并改成量「整段预算」，见下面的断言。
	const opTimeout = 20 * time.Millisecond

	db := &DB{timeout: opTimeout}
	// start 必须在这里取、且用 deadline.Sub(start) 而不是 time.Until(deadline)：
	// 要的就是「从调用前到 deadline」的整段预算，换成一个会移动的 now 就没意义了。
	start := time.Now()
	ctx, cancel := db.opCtx(t.Context())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("opCtx 必须带上 deadline，否则慢查询会一直占着连接与请求")
	}
	// budget = deadline - start = (opCtx 内部取时的时刻 - start) + opTimeout。
	// 内部那次取时必然不早于 start（单调钟不回退），所以下界 opTimeout 是硬不变量、
	// 与调度无关；上界则继续拦住「没按 db.timeout 设置、退化成写死的默认值」。
	if budget := deadline.Sub(start); budget < opTimeout || budget > time.Second {
		t.Fatalf("opCtx 的预算 = %v，期望落在 [%v, 1s]：它没有按 db.timeout 设置 deadline",
			budget, opTimeout)
	}

	<-ctx.Done()
	if cause := context.Cause(ctx); !errors.Is(cause, errOpTimeout) {
		t.Fatalf("context.Cause = %v, want %v", cause, errOpTimeout)
	}
	if !errors.Is(storageError(ctx.Err()), domain.ErrUnavailable) {
		t.Fatalf("op 超时产生的错误应归类为依赖不可用，实际 %v", ctx.Err())
	}
}
