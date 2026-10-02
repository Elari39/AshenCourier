package postgres

import (
	"ashen-courier/internal/domain"
	"sync"
	"testing"
	"uuid"
)

func TestCountBatchIdempotentConcurrentCommit(t *testing.T) {
	db := testDB(t)
	link := createLink(t, db, testCode(t, "batch"), nil)
	store := db.Links()
	batch := domain.CountBatch{ID: uuid.NewV7().String(), Delta: 7}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := store.ApplyCountBatch(t.Context(), link.ShortCode, batch); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := store.GetByCode(t.Context(), link.ShortCode)
	if err != nil || got.ClickCount != 7 {
		t.Fatalf("retry duplicated batch: %+v %v", got, err)
	}
	totals, err := store.CountBaselines(t.Context(), map[string]domain.CountSnapshot{link.ShortCode: {Batch: batch}})
	if err != nil || totals[link.ShortCode] != 7 {
		t.Fatalf("committed pending batch counted twice: %v %v", totals, err)
	}
	pending := domain.CountBatch{ID: uuid.NewV7().String(), Delta: 3}
	totals, err = store.CountBaselines(t.Context(), map[string]domain.CountSnapshot{link.ShortCode: {Batch: pending}})
	if err != nil || totals[link.ShortCode] != 10 {
		t.Fatalf("uncommitted batch lost: %v %v", totals, err)
	}
	if err := store.ApplyCountBatch(t.Context(), link.ShortCode, domain.CountBatch{ID: batch.ID, Delta: 8}); err == nil {
		t.Fatal("batch identity conflict accepted")
	}
}

func TestPasswordVersionChangesOnlyWithPassword(t *testing.T) {
	db := testDB(t)
	link := createLink(t, db, testCode(t, "version"), nil)
	store := db.Links()
	for i, patch := range []domain.LinkPatch{{PasswordHash: new("hash-one")}, {PasswordHash: new("hash-two")}, {PasswordHash: new("")}} {
		got, err := store.Update(t.Context(), link.ShortCode, patch)
		if err != nil || got.PasswordVersion != int64(i+1) {
			t.Fatalf("password version: %+v %v", got, err)
		}
	}
	got, err := store.Update(t.Context(), link.ShortCode, domain.LinkPatch{Title: new("new title")})
	if err != nil || got.PasswordVersion != 3 {
		t.Fatalf("title edit changed password version: %+v %v", got, err)
	}
}
