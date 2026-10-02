package service

import (
	"ashen-courier/internal/domain"
	"context"
)

type Totals struct {
	snapshots domain.CountSnapshotReader
	baselines domain.CountBaselineReader
}

func NewTotals(s domain.CountSnapshotReader, b domain.CountBaselineReader) *Totals {
	return &Totals{s, b}
}

func (t *Totals) TotalCounts(ctx context.Context, codes []string) (map[string]int64, error) {
	for range 3 {
		before, err := t.snapshots.CountSnapshots(ctx, codes)
		if err != nil {
			break
		}
		counts, err := t.baselines.CountBaselines(ctx, before)
		if err != nil {
			return nil, err
		}
		after, err := t.snapshots.CountSnapshots(ctx, codes)
		if err != nil {
			break
		}
		stable := true
		for _, code := range codes {
			// Epoch survives confirmation, detecting even a complete freeze/commit/ack cycle.
			if before[code].Epoch != after[code].Epoch || before[code].Batch != after[code].Batch {
				stable = false
				break
			}
		}
		if stable {
			for code, snap := range before {
				counts[code] += snap.Active
			}
			return counts, nil
		}
	}
	// Redis unavailable or continuously changing: return a fresh database baseline.
	empty := make(map[string]domain.CountSnapshot, len(codes))
	for _, code := range codes {
		empty[code] = domain.CountSnapshot{}
	}
	return t.baselines.CountBaselines(ctx, empty)
}
