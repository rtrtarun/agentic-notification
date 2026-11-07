package budget

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var start = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// contract is what every Budget must do. It runs on the memory budget, and on the
// Redis budget over a fake store that follows the Lua script's rules.
func contract(t *testing.T, newBudget func(limit int, window time.Duration, clk *clock) Budget) {
	t.Helper()
	ctx := context.Background()

	t.Run("allows up to the limit, then refuses and says when a slot frees", func(t *testing.T) {
		clk := &clock{t: start}
		b := newBudget(3, time.Hour, clk)
		for i := 1; i <= 3; i++ {
			clk.t = start.Add(time.Duration(i) * time.Minute)
			r, err := b.Take(ctx, ScopeWorkspace)
			if err != nil || !r.Allowed || r.Used != i || r.Limit != 3 || !r.ResetAt.IsZero() {
				t.Fatalf("take %d = %+v %v", i, r, err)
			}
		}
		r, err := b.Take(ctx, ScopeWorkspace)
		if err != nil || r.Allowed || r.Used != 3 {
			t.Fatalf("over the limit = %+v %v", r, err)
		}
		if want := start.Add(time.Minute).Add(time.Hour); !r.ResetAt.Equal(want) {
			t.Fatalf("ResetAt = %v, want %v (the oldest counted interruption plus the window)", r.ResetAt, want)
		}
	})

	t.Run("the window rolls: old interruptions stop counting", func(t *testing.T) {
		clk := &clock{t: start}
		b := newBudget(2, time.Hour, clk)
		b.Take(ctx, "s")
		clk.t = start.Add(30 * time.Minute)
		b.Take(ctx, "s")
		if r, _ := b.Take(ctx, "s"); r.Allowed {
			t.Fatal("full window must refuse")
		}
		clk.t = start.Add(61 * time.Minute) // the first one has left the window, the second has not
		r, _ := b.Take(ctx, "s")
		if !r.Allowed || r.Used != 2 {
			t.Fatalf("one slot should have freed: %+v", r)
		}
		if r, _ := b.Take(ctx, "s"); r.Allowed {
			t.Fatal("and only one")
		}
	})

	t.Run("scopes are counted separately", func(t *testing.T) {
		clk := &clock{t: start}
		b := newBudget(1, time.Hour, clk)
		if r, _ := b.Take(ctx, "user:alice"); !r.Allowed {
			t.Fatal("alice's first")
		}
		if r, _ := b.Take(ctx, "user:alice"); r.Allowed {
			t.Fatal("alice's second must be refused")
		}
		if r, _ := b.Take(ctx, "user:bob"); !r.Allowed {
			t.Fatal("bob has his own budget")
		}
	})

	t.Run("concurrent callers never exceed the limit", func(t *testing.T) {
		clk := &clock{t: start}
		b := newBudget(5, time.Hour, clk)
		var (
			mu      sync.Mutex
			allowed int
			wg      sync.WaitGroup
		)
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, err := b.Take(ctx, "race"); err == nil && r.Allowed {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if allowed != 5 {
			t.Fatalf("%d interruptions were allowed, want exactly 5", allowed)
		}
	})
}

func TestMemoryBudgetContract(t *testing.T) {
	contract(t, func(limit int, window time.Duration, clk *clock) Budget {
		return NewMemoryBudget(limit, window).WithClock(clk.now)
	})
}

func TestMemoryBudgetDefaultsAndCancelledContext(t *testing.T) {
	b := NewMemoryBudget(0, 0)
	if b.limit != DefaultPerHour || b.window != DefaultWindow {
		t.Fatalf("defaults wrong: %d %v", b.limit, b.window)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Take(ctx, "s"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// fakeStore follows the rules of the Lua script, so the glue around it is tested without Redis.
type fakeStore struct {
	mu      sync.Mutex
	entries map[string][]int64
	members map[string]bool
	err     error
	keys    []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[string][]int64{}, members: map[string]bool{}}
}

func (f *fakeStore) Slide(_ context.Context, key string, nowMs, windowMs int64, limit int, member string) (bool, int, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, 0, 0, f.err
	}
	f.keys = append(f.keys, key)
	kept := f.entries[key][:0]
	for _, ms := range f.entries[key] {
		if ms > nowMs-windowMs {
			kept = append(kept, ms)
		}
	}
	if len(kept) < limit {
		if f.members[member] {
			panic("the member must be unique per call")
		}
		f.members[member] = true
		kept = append(kept, nowMs)
		f.entries[key] = kept
		return true, len(kept), 0, nil
	}
	f.entries[key] = kept
	sort.Slice(kept, func(a, b int) bool { return kept[a] < kept[b] })
	return false, len(kept), kept[0], nil
}

func TestRedisBudgetContract(t *testing.T) {
	contract(t, func(limit int, window time.Duration, clk *clock) Budget {
		return NewRedisBudget(newFakeStore(), limit, window).WithClock(clk.now)
	})
}

func TestRedisBudgetKeysAreNamespacedPerScope(t *testing.T) {
	store := newFakeStore()
	b := NewRedisBudget(store, 2, time.Hour)
	b.Take(context.Background(), "user:alice")
	if len(store.keys) != 1 || store.keys[0] != "attention:budget:user:alice" {
		t.Fatalf("keys = %v", store.keys)
	}
}

func TestRedisBudgetReportsStoreErrorsSoTheCallerCanFailOpen(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New("connection refused")
	r, err := NewRedisBudget(store, 2, time.Hour).Take(context.Background(), "s")
	if err == nil || r.Allowed {
		t.Fatalf("an unreachable store must be an error, not a silent allow: %+v %v", r, err)
	}
}

func TestRedisBudgetDefaults(t *testing.T) {
	b := NewRedisBudget(newFakeStore(), -1, -1)
	if b.limit != DefaultPerHour || b.window != DefaultWindow {
		t.Fatalf("defaults wrong: %d %v", b.limit, b.window)
	}
}
