package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ashen-courier/internal/domain"
)

// DomainStore 是自定义域名的仓储实现。
type DomainStore struct {
	db *DB
}

// ListAll 读出全部已登记域名。
//
// 刻意**不分页**：域表是「配置表」性质，规模由运维决定（自建服务通常 1–3 行），
// 而每一个需要它的调用方都要全量 —— 分页只会让「拿全量」这件事变复杂，
// 却换不来任何东西。真到了需要分页的规模，这个接口本身就该重新设计。
func (s *DomainStore) ListAll(ctx context.Context) ([]domain.Domain, error) {
	const q = `SELECT id, domain, owner_id, verified_at, created_at FROM domains ORDER BY domain`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	rows, err := s.db.pool.Query(opCtx, q)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: list domains: %w", storageError(err))
	}
	defer rows.Close()

	out := make([]domain.Domain, 0, 8)
	for rows.Next() {
		var (
			id         pgtype.UUID
			name       string
			ownerID    pgtype.UUID
			verifiedAt pgtype.Timestamptz
			createdAt  time.Time
		)
		if err := rows.Scan(&id, &name, &ownerID, &verifiedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("store.postgres: scan domain: %w", storageError(err))
		}

		item := domain.Domain{
			ID:        fromPgUUID(id),
			Name:      name,
			OwnerID:   uuidPtr(ownerID),
			CreatedAt: createdAt,
		}
		if verifiedAt.Valid {
			t := verifiedAt.Time
			item.VerifiedAt = &t
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.postgres: iterate domains: %w", storageError(err))
	}
	return out, nil
}
