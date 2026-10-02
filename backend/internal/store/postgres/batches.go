package postgres

import (
	"ashen-courier/internal/domain"
	"context"
	"fmt"
)

// Batch registration and increment share a transaction. Retrying after an ambiguous
// commit observes the unique batch ID and cannot increment twice.
func (s *LinkStore) ApplyCountBatch(ctx context.Context, code string, batch domain.CountBatch) error {
	if batch.ID == "" || batch.Delta <= 0 {
		return fmt.Errorf("invalid count batch")
	}
	op, cancel := s.db.opCtx(ctx)
	defer cancel()
	tx, err := s.db.pool.Begin(op)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback(op)
	tag, err := tx.Exec(op, `INSERT INTO click_count_batches(batch_id, short_code, delta)
 VALUES ($1::uuid, $2, $3) ON CONFLICT (batch_id) DO NOTHING`, batch.ID, code, batch.Delta)
	if err != nil {
		return storageError(err)
	}
	if tag.RowsAffected() == 1 {
		tag, err = tx.Exec(op, `UPDATE links SET click_count = click_count + $2 WHERE short_code = $1`, code, batch.Delta)
		if err != nil {
			return storageError(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.NotFound("link", code)
		}
	} else {
		var matches bool
		err = tx.QueryRow(op, `SELECT short_code = $2 AND delta = $3 FROM click_count_batches WHERE batch_id = $1::uuid`, batch.ID, code, batch.Delta).Scan(&matches)
		if err != nil {
			return storageError(err)
		}
		if !matches {
			return fmt.Errorf("count batch identity conflict")
		}
	}
	return storageError(tx.Commit(op))
}

// One SQL snapshot reads both baseline and commit membership, preventing a
// committed batch from also being counted as pending.
func (s *LinkStore) CountBaselines(ctx context.Context, snapshots map[string]domain.CountSnapshot) (map[string]int64, error) {
	codes, ids, deltas := []string{}, []string{}, []int64{}
	for code, snap := range snapshots {
		codes = append(codes, code)
		ids = append(ids, snap.Batch.ID)
		deltas = append(deltas, snap.Batch.Delta)
	}
	op, cancel := s.db.opCtx(ctx)
	defer cancel()
	rows, err := s.db.pool.Query(op, `SELECT l.short_code, l.click_count + CASE WHEN b.batch_id IS NULL THEN x.delta ELSE 0 END
 FROM unnest($1::text[], $2::text[], $3::bigint[]) AS x(code, id, delta)
 JOIN links l ON l.short_code = x.code
 LEFT JOIN click_count_batches b ON b.batch_id = NULLIF(x.id, '')::uuid`, codes, ids, deltas)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	out := make(map[string]int64, len(codes))
	for rows.Next() {
		var code string
		var count int64
		if err := rows.Scan(&code, &count); err != nil {
			return nil, storageError(err)
		}
		out[code] = count
	}
	return out, storageError(rows.Err())
}
