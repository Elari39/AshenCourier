package redis

import (
	"sync"
	"testing"
	"uuid"
)

func TestBatchRetryConcurrentFreezeAndStaleConfirm(t *testing.T) {
	c := openTestClient(t)
	code := "batch-" + uuid.NewV7().String()
	ctx := t.Context()
	t.Cleanup(func() {
		c.rdb.Del(ctx, ClickCounterKey(code), batchKey(code), epochKey(code))
		c.rdb.SRem(ctx, dirtySetKey, code)
	})
	if err := c.rdb.Set(ctx, ClickCounterKey(code), 7, 0).Err(); err != nil {
		t.Fatal(err)
	}
	c.MarkDirty(ctx, code)
	first, err := c.FreezeBatch(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if first.Delta != 7 || first.ID == "" {
		t.Fatalf("invalid first batch: %+v", first)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			b, err := c.FreezeBatch(ctx, code)
			if err != nil || b != first {
				t.Errorf("retry changed batch: %+v %v", b, err)
			}
		})
	}
	wg.Wait()
	c.rdb.IncrBy(ctx, ClickCounterKey(code), 3)
	snapshots, err := c.CountSnapshots(ctx, []string{code})
	if err != nil {
		t.Fatal(err)
	}
	if snapshots[code].Active != 3 || snapshots[code].Batch != first {
		t.Fatalf("snapshot: %+v", snapshots)
	}
	if err := c.ConfirmBatch(ctx, code, first); err != nil {
		t.Fatal(err)
	}
	second, err := c.FreezeBatch(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Delta != 3 {
		t.Fatalf("second batch: %+v", second)
	}
	if err := c.ConfirmBatch(ctx, code, first); err != nil {
		t.Fatal(err)
	}
	again, err := c.FreezeBatch(ctx, code)
	if err != nil || again != second {
		t.Fatalf("stale confirm removed new batch: %+v %v", again, err)
	}
	if err := c.ConfirmBatch(ctx, code, second); err != nil {
		t.Fatal(err)
	}
	if isDirty(t, c, code) {
		t.Fatal("confirmed counter remains dirty")
	}
}
