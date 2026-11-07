package overrides

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

	t.Run("an override is stored with its scope and who made it", func(t *testing.T) {
		saved, err := s.Save(ctx, Override{ID: "o1", Source: "github", EventType: "star", Mode: ModeNever, CreatedBy: "alice", Note: "noise", CreatedAt: contractNow})
		if err != nil || saved.ID == "" {
			t.Fatalf("save: %+v %v", saved, err)
		}
		all, err := s.Load(ctx)
		if err != nil || len(all) != 1 {
			t.Fatalf("load = %+v %v", all, err)
		}
		got := all[0]
		if got.Source != "github" || got.EventType != "star" || got.Mode != ModeNever || got.CreatedBy != "alice" || got.Note != "noise" {
			t.Fatalf("stored wrongly: %+v", got)
		}
	})

	t.Run("saving the same scope again changes the mode instead of adding a second override", func(t *testing.T) {
		if _, err := s.Save(ctx, Override{ID: "o2", Source: "github", EventType: "star", Mode: ModeAlways, CreatedBy: "bob", CreatedAt: contractNow.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		all, _ := s.Load(ctx)
		if len(all) != 1 || all[0].Mode != ModeAlways {
			t.Fatalf("same scope must replace the mode, got %+v", all)
		}
	})

	t.Run("different scopes coexist, including repository wildcards", func(t *testing.T) {
		if _, err := s.Save(ctx, Override{ID: "o3", Source: "github", Repository: "acme/*", Mode: ModeNever, CreatedBy: "alice", CreatedAt: contractNow}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Save(ctx, Override{ID: "o4", Source: "github", EventType: "push", Repository: "acme/api", Mode: ModeAlways, CreatedBy: "alice", CreatedAt: contractNow}); err != nil {
			t.Fatal(err)
		}
		all, _ := s.Load(ctx)
		if len(all) != 3 {
			t.Fatalf("want 3 scopes, got %+v", all)
		}
	})

	t.Run("a deleted override is gone and a second delete removes nothing else", func(t *testing.T) {
		all, _ := s.Load(ctx)
		id := all[0].ID
		if err := s.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
		after, _ := s.Load(ctx)
		if len(after) != len(all)-1 {
			t.Fatalf("delete did not remove: %d -> %d", len(all), len(after))
		}
		_ = s.Delete(ctx, id) // an error is allowed here; removing something else is not
		if again, _ := s.Load(ctx); len(again) != len(after) {
			t.Fatalf("a second delete removed something else: %d -> %d", len(after), len(again))
		}
	})
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }
