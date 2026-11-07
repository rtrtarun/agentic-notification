package feedback

import (
	"context"
	"testing"
	"time"
)

var contractNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// storeContract is the behaviour every Store must have. It runs against the
// memory store always and against PostgreSQL in the integration test.
func storeContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	entries := []EventFeedback{
		{ID: "f1", EventID: "e1", UserID: "alice", Action: "dismissed", Source: "github", EventType: "star", Repository: "acme/web", CreatedAt: contractNow},
		{ID: "f2", EventID: "e1", UserID: "bob", Action: "opened", Source: "github", EventType: "star", Repository: "acme/web", CreatedAt: contractNow.Add(time.Minute)},
		{ID: "f3", EventID: "e2", UserID: "alice", Action: "always", Scope: "repository", Reason: "matters", OverrideID: "o1", Source: "github", EventType: "push", Repository: "acme/api", CreatedAt: contractNow.Add(2 * time.Minute)},
	}
	for _, f := range entries {
		if err := s.Add(ctx, f); err != nil {
			t.Fatalf("add %s: %v", f.ID, err)
		}
	}

	ids := func(list []EventFeedback) string {
		out := ""
		for _, f := range list {
			out += f.ID + " "
		}
		return out
	}

	t.Run("everything comes back newest first with its attribution", func(t *testing.T) {
		all, err := s.List(ctx, Filter{})
		if err != nil || ids(all) != "f3 f2 f1 " {
			t.Fatalf("list = %q %v", ids(all), err)
		}
		got := all[0]
		if got.UserID != "alice" || got.Scope != "repository" || got.Reason != "matters" || got.OverrideID != "o1" ||
			got.Source != "github" || got.EventType != "push" || got.Repository != "acme/api" || !got.CreatedAt.Equal(entries[2].CreatedAt) {
			t.Fatalf("attribution lost: %+v", got)
		}
	})

	t.Run("filters by event and by person", func(t *testing.T) {
		if got, _ := s.List(ctx, Filter{EventID: "e1"}); ids(got) != "f2 f1 " {
			t.Fatalf("by event = %q", ids(got))
		}
		if got, _ := s.List(ctx, Filter{UserID: "alice"}); ids(got) != "f3 f1 " {
			t.Fatalf("by user = %q", ids(got))
		}
		if got, _ := s.List(ctx, Filter{EventID: "e1", UserID: "alice"}); ids(got) != "f1 " {
			t.Fatalf("by both = %q", ids(got))
		}
		if got, _ := s.List(ctx, Filter{EventID: "none"}); len(got) != 0 {
			t.Fatalf("unknown event = %q", ids(got))
		}
	})

	t.Run("filters by time and limits the count", func(t *testing.T) {
		if got, _ := s.List(ctx, Filter{Since: contractNow.Add(30 * time.Second)}); ids(got) != "f3 f2 " {
			t.Fatalf("since = %q", ids(got))
		}
		if got, _ := s.List(ctx, Filter{Limit: 1}); ids(got) != "f3 " {
			t.Fatalf("limit = %q", ids(got))
		}
	})
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }
