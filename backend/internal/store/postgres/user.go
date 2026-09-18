package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ashen-courier/internal/domain"
	"uuid"
)

// UserStore 实现 domain.UserRepository，
// 与 DB 共用同一个连接池，拆成独立类型是为了避免不同实体的同名方法互相覆盖。
type UserStore struct {
	db *DB
}

// userColumns 是 users 表的统一列顺序。
const userColumns = `id, email, password_hash, display_name, created_at, updated_at`

// userRow 是 users 表的一行。
// id 用 pgtype.UUID 而不是领域层的 uuid.UUID：与 linkRow 保持一致，
// 也遵守本包 db.go 的约定 —— 不依赖 pgx 对「底层为 [16]byte 的命名类型」
// 的隐式 scan plan 识别。
type userRow struct {
	id           pgtype.UUID
	email        string
	passwordHash string
	displayName  string
	createdAt    time.Time
	updatedAt    time.Time
}

// dest 返回交给 Scan 的目标，顺序与 userColumns 一致。
func (r *userRow) dest() []any {
	return []any{&r.id, &r.email, &r.passwordHash, &r.displayName, &r.createdAt, &r.updatedAt}
}

// toDomain 把行数据转成领域实体。
func (r *userRow) toDomain() *domain.User {
	return &domain.User{
		ID:           fromPgUUID(r.id),
		Email:        r.email,
		PasswordHash: r.passwordHash,
		DisplayName:  r.displayName,
		CreatedAt:    r.createdAt,
		UpdatedAt:    r.updatedAt,
	}
}

// Create 插入用户。邮箱唯一索引是 lower(email)，冲突时返回 Field="email" 的领域错误。
func (s *UserStore) Create(ctx context.Context, u *domain.User) error {
	const q = `
INSERT INTO users (id, email, password_hash, display_name, created_at, updated_at)
VALUES ($1, $2, $3, $4, now(), now())
RETURNING created_at, updated_at`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	err := s.db.pool.QueryRow(opCtx, q,
		toPgUUID(u.ID), u.Email, u.PasswordHash, u.DisplayName,
	).Scan(&u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store.postgres: create user: %w", mapWriteError(err, "email", u.Email))
	}
	return nil
}

// GetByEmail 按不区分大小写的邮箱查询。
func (s *UserStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE lower(email) = lower($1)`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var r userRow
	if err := s.db.pool.QueryRow(opCtx, q, email).Scan(r.dest()...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("user", email)
		}
		return nil, fmt.Errorf("store.postgres: get user by email: %w", storageError(err))
	}
	return r.toDomain(), nil
}

// GetByID 按主键查询。
func (s *UserStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var r userRow
	if err := s.db.pool.QueryRow(opCtx, q, toPgUUID(id)).Scan(r.dest()...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("user", id.String())
		}
		return nil, fmt.Errorf("store.postgres: get user by id: %w", storageError(err))
	}
	return r.toDomain(), nil
}
