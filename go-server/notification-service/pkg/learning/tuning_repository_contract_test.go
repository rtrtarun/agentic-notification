package learning

import (
	"context"
	"errors"
	"testing"
	"time"
)

// adjustmentContract is the behaviour every AdjustmentRepository must have. It runs against the memory
// repository always and against PostgreSQL in the integration test.
func adjustmentContract(t *testing.T, r AdjustmentRepository) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.Get(ctx, "nope"); !errors.Is(err, ErrAdjustmentNotFound) {
		t.Fatalf("missing adjustment: %v", err)
	}
	if v, err := r.MaxVersion(ctx); err != nil || v != 0 {
		t.Fatalf("an empty repository has version 0: %d %v", v, err)
	}
	if list, err := r.List(ctx, ""); err != nil || len(list) != 0 {
		t.Fatalf("a new repository must be empty: %v %v", list, err)
	}

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	resolved := base.Add(3 * time.Hour)
	items := []Adjustment{
		{ID: "a1", Source: "github", EventType: "star", Delta: -14, Status: AdjustmentProposed,
			Evidence: TuneEvidence{Positive: 0, Negative: 5, People: 2, Signals: 5, WindowDays: 30, Summary: "5 of 5 signals from 2 people were negative"},
			Impact:   Impact{Replayed: 9, WouldChange: 4}, CreatedAt: base, UpdatedAt: base},
		{ID: "a2", Source: "github", EventType: "push", Repository: "acme/web", Delta: 9, Status: AdjustmentProposed, CreatedAt: base, UpdatedAt: base.Add(time.Hour)},
		{ID: "a3", Source: "github", EventType: "issues", Delta: -6, Status: AdjustmentActive, Version: 3, ResolvedBy: "admin", ResolvedAt: &resolved,
			CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
	}
	for _, a := range items {
		if err := r.Save(ctx, a); err != nil {
			t.Fatalf("save %s: %v", a.ID, err)
		}
	}

	t.Run("an adjustment round-trips with its evidence, impact and resolution", func(t *testing.T) {
		got, err := r.Get(ctx, "a1")
		if err != nil || got.Delta != -14 || got.Evidence != items[0].Evidence || got.Impact != items[0].Impact || got.ResolvedAt != nil || got.Repository != "" {
			t.Fatalf("a1 = %+v %v", got, err)
		}
		active, _ := r.Get(ctx, "a3")
		if active.Status != AdjustmentActive || active.Version != 3 || active.ResolvedBy != "admin" || active.ResolvedAt == nil || !active.ResolvedAt.Equal(resolved) {
			t.Fatalf("a3 = %+v", active)
		}
		repo, _ := r.Get(ctx, "a2")
		if repo.Repository != "acme/web" || repo.Delta != 9 {
			t.Fatalf("a2 = %+v", repo)
		}
	})

	t.Run("listing filters by status and puts the most recently updated first", func(t *testing.T) {
		all, _ := r.List(ctx, "")
		if len(all) != 3 || all[0].ID != "a3" || all[2].ID != "a1" {
			t.Fatalf("all = %+v", all)
		}
		proposed, _ := r.List(ctx, AdjustmentProposed)
		if len(proposed) != 2 || proposed[0].ID != "a2" {
			t.Fatalf("proposed = %+v", proposed)
		}
		if none, _ := r.List(ctx, AdjustmentRetired); len(none) != 0 {
			t.Fatalf("retired = %+v", none)
		}
	})

	t.Run("the highest version is reported", func(t *testing.T) {
		if v, _ := r.MaxVersion(ctx); v != 3 {
			t.Fatalf("version = %d", v)
		}
	})

	t.Run("saving again replaces it and never adds a row", func(t *testing.T) {
		a := items[0]
		a.Status, a.Delta, a.Version, a.UpdatedAt = AdjustmentActive, -11, 4, base.Add(5*time.Hour)
		if err := r.Save(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, _ := r.Get(ctx, "a1")
		if got.Status != AdjustmentActive || got.Delta != -11 || got.Version != 4 {
			t.Fatalf("a1 = %+v", got)
		}
		if all, _ := r.List(ctx, ""); len(all) != 3 {
			t.Fatalf("rows = %d", len(all))
		}
		if v, _ := r.MaxVersion(ctx); v != 4 {
			t.Fatalf("version = %d", v)
		}
	})
}

// runContract is the behaviour every RunRepository must have.
func runContract(t *testing.T, r RunRepository) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	if list, err := r.List(ctx, 10); err != nil || len(list) != 0 {
		t.Fatalf("a new repository must be empty: %v %v", list, err)
	}
	first := Run{ID: "r1", Status: RunRunning, StartedAt: base}
	if err := r.Start(ctx, first, base.Add(-StaleRunAfter)); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, Run{ID: "r2", Status: RunRunning, StartedAt: base.Add(time.Second)}, base.Add(time.Second-StaleRunAfter)); !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("only one run may be running: %v", err)
	}

	end := base.Add(time.Minute)
	first.Status, first.FinishedAt, first.FeedbackRead, first.EventsRead, first.ProposalsMade = RunDone, &end, 40, 900, 2
	if err := r.Finish(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := Run{ID: "r2", Status: RunRunning, StartedAt: base.Add(time.Hour)}
	if err := r.Start(ctx, second, base.Add(time.Hour-StaleRunAfter)); err != nil {
		t.Fatalf("a run can start once the last finished: %v", err)
	}
	second.Status, second.FinishedAt, second.Error = RunFailed, &end, "read feedback"
	if err := r.Finish(ctx, second); err != nil {
		t.Fatal(err)
	}

	list, _ := r.List(ctx, 10)
	if len(list) != 2 || list[0].ID != "r2" || list[1].ID != "r1" {
		t.Fatalf("newest first: %+v", list)
	}
	if got := list[1]; got.Status != RunDone || got.FeedbackRead != 40 || got.EventsRead != 900 || got.ProposalsMade != 2 || got.FinishedAt == nil || !got.FinishedAt.Equal(end) {
		t.Fatalf("r1 = %+v", got)
	}
	if got := list[0]; got.Status != RunFailed || got.Error != "read feedback" {
		t.Fatalf("r2 = %+v", got)
	}
	if one, _ := r.List(ctx, 1); len(one) != 1 || one[0].ID != "r2" {
		t.Fatalf("limit: %+v", one)
	}

	// A run left running long ago is marked failed when the next one starts.
	stuck := Run{ID: "r3", Status: RunRunning, StartedAt: base.Add(2 * time.Hour)}
	if err := r.Start(ctx, stuck, base.Add(2*time.Hour-StaleRunAfter)); err != nil {
		t.Fatal(err)
	}
	next := Run{ID: "r4", Status: RunRunning, StartedAt: base.Add(2*time.Hour + StaleRunAfter + time.Minute)}
	if err := r.Start(ctx, next, next.StartedAt.Add(-StaleRunAfter)); err != nil {
		t.Fatalf("an abandoned run must not block: %v", err)
	}
	all, _ := r.List(ctx, 10)
	for _, run := range all {
		if run.ID == "r3" && (run.Status != RunFailed || run.Error != abandonedRun || run.FinishedAt == nil) {
			t.Fatalf("r3 = %+v", run)
		}
	}
}

func TestMemoryAdjustmentsContract(t *testing.T) { adjustmentContract(t, NewMemoryAdjustments()) }
func TestMemoryRunsContract(t *testing.T)        { runContract(t, NewMemoryRuns()) }
