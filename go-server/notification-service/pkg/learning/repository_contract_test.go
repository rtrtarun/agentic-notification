package learning

import (
	"context"
	"errors"
	"testing"
	"time"
)

// repositoryContract is the behaviour every Repository must have. It runs
// against the memory repository always and against PostgreSQL in the integration test.
func repositoryContract(t *testing.T, r Repository) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing suggestion: %v", err)
	}
	if list, err := r.List(ctx, ""); err != nil || len(list) != 0 {
		t.Fatalf("a new repository must be empty: %v %v", list, err)
	}

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	resolved := base.Add(3 * time.Hour)
	items := []Suggestion{
		{ID: "s1", Kind: KindAdaptive, Status: StatusPending, Source: "github", EventType: "star", RuleID: "r1", RuleName: "repo-activity",
			Summary: "sum", Proposed: "prop", Evidence: Evidence{Dismissed: 5, Never: 1}, CreatedAt: base, UpdatedAt: base},
		{ID: "s2", Kind: KindOverride, Status: StatusPending, Source: "github", EventType: "fork", Repository: "acme/web",
			Evidence: Evidence{Never: 6}, CreatedAt: base, UpdatedAt: base.Add(time.Hour)},
		{ID: "s3", Kind: KindPromote, Status: StatusApplied, Source: "github", EventType: "push", ResolvedBy: "admin", ResolvedAt: &resolved,
			Evidence: Evidence{Always: 3}, CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
	}
	for _, s := range items {
		if err := r.Save(ctx, s); err != nil {
			t.Fatalf("save %s: %v", s.ID, err)
		}
	}

	t.Run("a suggestion round-trips with its evidence and resolution", func(t *testing.T) {
		got, err := r.Get(ctx, "s1")
		if err != nil || got.Kind != KindAdaptive || got.RuleID != "r1" || got.Evidence.Dismissed != 5 || got.Evidence.Never != 1 || got.Summary != "sum" {
			t.Fatalf("s1 = %+v %v", got, err)
		}
		done, _ := r.Get(ctx, "s3")
		if done.Status != StatusApplied || done.ResolvedBy != "admin" || done.ResolvedAt == nil || !done.ResolvedAt.Equal(resolved) {
			t.Fatalf("s3 = %+v", done)
		}
	})

	t.Run("listing filters by status and puts the most recently updated first", func(t *testing.T) {
		all, _ := r.List(ctx, "")
		if len(all) != 3 || all[0].ID != "s3" || all[2].ID != "s1" {
			t.Fatalf("all = %+v", all)
		}
		pending, _ := r.List(ctx, StatusPending)
		if len(pending) != 2 || pending[0].ID != "s2" {
			t.Fatalf("pending = %+v", pending)
		}
		if none, _ := r.List(ctx, StatusDismissed); len(none) != 0 {
			t.Fatalf("dismissed = %+v", none)
		}
	})

	t.Run("saving again replaces the suggestion", func(t *testing.T) {
		s := items[0]
		s.Status, s.UpdatedAt = StatusDismissed, base.Add(5*time.Hour)
		if err := r.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
		got, _ := r.Get(ctx, "s1")
		if got.Status != StatusDismissed {
			t.Fatalf("status = %s", got.Status)
		}
		if all, _ := r.List(ctx, ""); len(all) != 3 {
			t.Fatalf("saving the same ID must not add a row: %d", len(all))
		}
		if pending, _ := r.List(ctx, StatusPending); len(pending) != 1 {
			t.Fatalf("pending = %+v", pending)
		}
	})
}
