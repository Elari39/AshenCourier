package service

import (
	"ashen-courier/internal/domain"
	"context"
	"testing"
)

type changingSnapshots struct{ calls int }

func (s *changingSnapshots) CountSnapshots(context.Context, []string) (map[string]domain.CountSnapshot, error) {
	s.calls++
	if s.calls == 1 {
		return map[string]domain.CountSnapshot{"code": {Active: 7}}, nil
	}
	return map[string]domain.CountSnapshot{"code": {Active: 2, Epoch: "committed"}}, nil
}

type countBaseline struct{}

func (countBaseline) CountBaselines(context.Context, map[string]domain.CountSnapshot) (map[string]int64, error) {
	return map[string]int64{"code": 7}, nil
}
func TestTotalsRetriesTransferDuringRead(t *testing.T) {
	s := &changingSnapshots{}
	counts, err := NewTotals(s, countBaseline{}).TotalCounts(t.Context(), []string{"code"})
	if err != nil || counts["code"] != 9 || s.calls != 4 {
		t.Fatalf("counts=%v calls=%d err=%v", counts, s.calls, err)
	}
}
