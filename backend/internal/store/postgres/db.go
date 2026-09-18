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
	"context"
	"errors"
	"fmt"
	"time"

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

// DB 持有连接池，并派生三个仓储实现。
type DB struct {
	pool *pgxpool.Pool
}

// 编译期断言：三个仓储接口都有对应实现。
var (
	_ domain.LinkRepository  = (*LinkStore)(nil)
	_ domain.UserRepository  = (*UserStore)(nil)
	_ domain.ClickRepository = (*ClickStore)(nil)
)

// Open 创建连接池并做一次 Ping 验证；调用方负责在退出时 Close。
func Open(ctx context.Context, dsn string, maxConns int32) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: parse dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
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
	return &DB{pool: pool}, nil
}

// Close 关闭连接池。
func (db *DB) Close() {
	db.pool.Close()
}

// Ping 做一次连通性探测，供 /healthz 使用。
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
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
		return err
	}
}
