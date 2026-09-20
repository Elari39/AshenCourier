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
