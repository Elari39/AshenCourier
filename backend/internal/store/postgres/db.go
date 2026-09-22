// Package postgres 实现 internal/domain 中定义的三个仓储接口。
//
// 设计取舍：
//   - 不用 ORM，手写 SQL。本项目查询不到 20 条，SQL 直接可读可调。
//   - 领域层用标准库 uuid.UUID；这里统一转换到 pgtype.UUID 作为参数与扫描目标，
//     不依赖 pgx 对 [16]byte 命名类型的隐式识别。
//   - 驱动错误统一翻译成领域错误（domain.Conflict / domain.NotFound / domain.Invalid），
//     上层只认领域错误，不 import pgx。
//   - LinkStore / UserStore / ClickStore 是三个独立类型而不是一个 DB 上的方法集：
//     它们的 Create 等方法同名，挂在同一个类型上会互相覆盖。
package postgres

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ashen-courier/internal/domain"
	"uuid"
)

// uniqueViolation 是 PostgreSQL 唯一约束冲突的 SQLSTATE。
const uniqueViolation = "23505"

// checkViolation 是 PostgreSQL CHECK 约束冲突的 SQLSTATE。
const checkViolation = "23514"

// errOpTimeout 是「PostgreSQL 操作超时」的取消原因，与 store/redis 的同名哨兵对称：
// 日志里因此能区分「库慢」与「上游把请求取消了」。
var errOpTimeout = errors.New("postgres operation timeout")

// defaultOpTimeout 是 Options.OpTimeout 的缺省值，与 config.DefaultPGTimeout 一致。
const defaultOpTimeout = 3 * time.Second

// Options 是 Open 的入参，形状对齐 store/redis.Options。
type Options struct {
	// DSN 是 PostgreSQL 连接串。
	DSN string
	// MaxConns 是连接池上限，<=0 时用 pgx 默认值。
	MaxConns int32
	// OpTimeout 是单次查询的超时，<=0 时取 defaultOpTimeout。
	OpTimeout time.Duration
}

// DB 持有连接池，并派生三个仓储实现。
type DB struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

// 编译期断言：三个仓储接口都有对应实现；
// worker 只依赖后两个窄切片，断言保证放开依赖后仍由同一实现满足。
var (
	_ domain.LinkRepository     = (*LinkStore)(nil)
	_ domain.UserRepository     = (*UserStore)(nil)
	_ domain.ClickRepository    = (*ClickStore)(nil)
	_ domain.ClickCountWriter   = (*LinkStore)(nil)
	_ domain.ExpiredLinkSweeper = (*LinkStore)(nil)
)

// Open 创建连接池并做一次 Ping 验证；调用方负责在退出时 Close。
func Open(ctx context.Context, opt Options) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(opt.DSN)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: parse dsn: %w", err)
	}
	if opt.MaxConns > 0 {
		cfg.MaxConns = opt.MaxConns
	}
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store.postgres: ping: %w", err)
	}
	return &DB{pool: pool, timeout: cmp.Or(opt.OpTimeout, defaultOpTimeout)}, nil
}

// opCtx 为单次查询套上超时，并记录取消原因。
//
// 刻意不给 Open / Ping 使用：启动探测与健康探针的预算由调用方给定，
// 再叠加一层 op 超时只会让「探针超时」与「库慢」两个结论互相掩盖。
func (db *DB) opCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, db.timeout, errOpTimeout)
}

// Close 关闭连接池。
func (db *DB) Close() {
	db.pool.Close()
}

// Ping 做一次连通性探测，供 /healthz 使用。超时预算由调用方通过 ctx 给定。
func (db *DB) Ping(ctx context.Context) error {
	if err := db.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store.postgres: ping: %w", err)
	}
	return nil
}

// Links 返回短链仓储。
func (db *DB) Links() *LinkStore { return &LinkStore{db: db} }

// Users 返回账号仓储。
func (db *DB) Users() *UserStore { return &UserStore{db: db} }

// Clicks 返回点击明细仓储。
func (db *DB) Clicks() *ClickStore { return &ClickStore{db: db} }

// Domains 返回自定义域名仓储。
func (db *DB) Domains() *DomainStore { return &DomainStore{db: db} }

// toPgUUID 把领域层 UUID 转成 pgx 参数。
func toPgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(u), Valid: true}
}

// uuidParam 把可空 UUID 转成 pgx 参数；nil 映射为 SQL NULL。
func uuidParam(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*u), Valid: true}
}

// fromPgUUID 把扫描结果转回领域层 UUID；NULL 得到 uuid.Nil()。
func fromPgUUID(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil()
	}
	return uuid.UUID(p.Bytes)
}

// uuidPtr 把可空扫描结果转成 *uuid.UUID；NULL 得到 nil。
func uuidPtr(p pgtype.UUID) *uuid.UUID {
	if !p.Valid {
		return nil
	}
	u := uuid.UUID(p.Bytes)
	return &u
}

// pgErrCode 提取 PostgreSQL 错误码，非 PG 错误返回空串。
func pgErrCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

// pgUnavailableCode 判断一个 SQLSTATE 是否表示「数据库**暂时**不可用」。
//
// 这条界线是整个 storageError 的关键：带 SQLSTATE 只是一个信号，不是结论。
// 「数据库暂时不可用」该回 503 + Retry-After（客户端与上游据此退避），
// 「数据库健康、是这次请求/数据的问题」该回 4xx/500（重试无用）。
//
// 判断按**类**而不是逐个码枚举：类里每个码同属一种故障，逐码枚举会在服务端
// 新增码时悄悄退回 500 —— 那正是这个分类要修的毛病。三类：
//
//   - 类 08 connection_exception：连接建立 / 断开 / 协议错（08000 / 08001 /
//     08003 / 08004 / 08006 / 08007 / 08P01）。库多半只是重启了一下。
//   - 类 53 insufficient_resources：服务端资源耗尽（53300 连接数打满、
//     53100 磁盘满、53200 OOM、53400 触到配置上限）。过载期最需要退避，
//     也恰恰是最容易被回成 500 的时刻。
//   - 57P01 admin_shutdown / 57P02 crash_shutdown / 57P03 cannot_connect_now：
//     服务端正在关停或还没起来（57P03 就是启动期探测遇到的那个码）。
//     ⚠️ 只认这三个而**不是整类 57** —— 57014 query_canceled（多半是
//     statement_timeout）、57P04 database_dropped 的成因与「暂时不可用」不同。
//
// 刻意**不含** 40001 serialization_failure / 40P01 deadlock_detected：那是
// 「库健康、这次事务撞车了」，正确做法是应用层重试**整个请求**，而不是把服务
// 标记成不可用 —— 回 503 会让上游连累同一实例上与之无关的其它请求一起退避。
func pgUnavailableCode(code string) bool {
	switch {
	case code == "":
		return false
	case strings.HasPrefix(code, "08"), strings.HasPrefix(code, "53"):
		return true
	case code == "57P01", code == "57P02", code == "57P03":
		return true
	default:
		return false
	}
}

// storageError 把「不是数据库给出的业务错误」归类为依赖不可用（可重试）。
//
// 判定规则：
//   - 拿得到 SQLSTATE 且码属于 pgUnavailableCode（连接异常 / 资源耗尽 / 服务端
//     正在关停-启动）→ 数据库暂时不可用，包上 domain.ErrUnavailable，
//     由 httpx 映射成 503 + Retry-After
//   - 拿得到 SQLSTATE 但码不在其中（约束冲突、语法错误、数据异常……）→
//     数据库本体是健康的，属于请求或数据问题，原样上抛（4xx/500）
//   - 拿不到 SQLSTATE 的失败也是「依赖不可用」——拨号失败、连接被断、池已关闭、
//     context 超时——同样包上 domain.ErrUnavailable
//   - pgx.ErrNoRows 是「查无此行」的正常分支，不算故障
//   - context.Canceled（客户端断连 / 进程关停）也算可重试：此刻没有调用方在读响应，
//     503 不会产生副作用，却避免了把「客户端跑了」记成 500 的错误日志噪音
//
// 用双 %w 同时保留哨兵与底层错误：errors.Is(err, domain.ErrUnavailable) 与
// errors.Is(err, 原始错误) 都成立，日志里也不会丢掉根因。
func storageError(err error) error {
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if code := pgErrCode(err); code != "" && !pgUnavailableCode(code) {
		return err
	}
	return fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
}

// mapWriteError 把写入类错误翻译成领域错误。
// conflictField 是发生 23505 时应归因的字段。
func mapWriteError(err error, conflictField, conflictValue string) error {
	switch pgErrCode(err) {
	case uniqueViolation:
		return domain.Conflict(conflictField, conflictValue)
	case checkViolation:
		return domain.Invalid("target_url", "URL 不满足数据库约束（仅允许 http/https，且长度不超过 2048）")
	default:
		return storageError(err)
	}
}
