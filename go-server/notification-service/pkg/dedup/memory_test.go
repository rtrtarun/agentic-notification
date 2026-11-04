package dedup

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMemoryForgetAllowsRetry(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	first := check(d, prEvent("a", 1, "opened"))
	if err := d.Forget(context.Background(), first.DedupKey, "a"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if check(d, prEvent("a-retry", 1, "opened")).Duplicate {
		t.Fatal("retry after Forget must not be a duplicate")
	}
}

func TestMemoryForgetKeepsOtherEventsEntry(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	first := check(d, prEvent("a", 1, "opened"))
	check(d, prEvent("b", 1, "opened"))
	d.Forget(context.Background(), first.DedupKey, "b")
	if third := check(d, prEvent("c", 1, "opened")); !third.Duplicate || third.OriginalID != "a" {
		t.Fatalf("entry owned by a must survive Forget by b: %+v", third)
	}
}

func TestMemorySweepDropsExpiredEntries(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	now := time.Now()
	d.now = func() time.Time { return now }
	for i := 0; i < 50; i++ {
		check(d, prEvent("e", float64(i), "opened"))
	}
	if len(d.seen) != 50 {
		t.Fatalf("expected 50 entries, got %d", len(d.seen))
	}

	d.now = func() time.Time { return now.Add(3 * time.Minute) }
	check(d, prEvent("later", 999, "opened"))
	if len(d.seen) != 1 {
		t.Fatalf("expired entries should be swept, got %d", len(d.seen))
	}
}

func TestMemoryDoesNotSweepOnEveryCheck(t *testing.T) {
	d := NewMemoryDeduper(time.Hour)
	now := time.Now()
	d.now = func() time.Time { return now }
	check(d, prEvent("a", 1, "opened"))
	next := d.nextSweep

	d.now = func() time.Time { return now.Add(time.Second) }
	check(d, prEvent("b", 2, "opened"))
	if !d.nextSweep.Equal(next) {
		t.Fatal("sweep ran again before half a window elapsed")
	}
}

func TestMemoryStopsRecordingWhenFullButNeverSuppresses(t *testing.T) {
	d := NewMemoryDeduper(time.Hour)
	for i := 0; i < maxMemoryEntries; i++ {
		d.seen[fmt.Sprintf("k%d", i)] = seenEntry{id: "x", at: time.Now()}
	}

	res := check(d, prEvent("new", 1, "opened"))
	if res.Duplicate {
		t.Fatal("a full table must not mark new events as duplicates")
	}
	if len(d.seen) != maxMemoryEntries {
		t.Fatalf("table grew past its cap: %d", len(d.seen))
	}
}
