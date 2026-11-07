package attention

import (
	"context"
	"errors"
	"testing"
	"time"
)

var contractNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// digestStoreContract is the behaviour every DigestStore must have. It runs
// against the memory store always and against PostgreSQL in the integration test.
func digestStoreContract(t *testing.T, s DigestStore) {
	t.Helper()
	ctx := context.Background()

	if list, err := s.List(ctx, 10); err != nil || len(list) != 0 {
		t.Fatalf("a new store must be empty: %v %v", list, err)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrDigestNotFound) {
		t.Fatalf("missing digest: %v", err)
	}

	for i, id := range []string{"d1", "d2", "d3"} {
		at := contractNow.Add(time.Duration(i) * time.Hour)
		d := Digest{
			ID: id, From: at.Add(-time.Hour), To: at, Count: i + 1, Title: "title " + id, Summary: "summary", Model: "ollama:llama3.2", Source: "model",
			Groups:     []DigestGroup{{Key: "github/acme/web", Title: "acme/web", Summary: "x", Count: i + 1, EventIDs: []string{"e1", "e2"}}},
			Highlights: []Highlight{{EventID: "e1", Title: "pr", Reason: "scored 75", Score: 75}},
			CreatedBy:  "schedule", CreatedAt: at,
		}
		if err := s.Add(ctx, d); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	t.Run("a digest round-trips with its groups, highlights and author", func(t *testing.T) {
		got, err := s.Get(ctx, "d2")
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "title d2" || got.Count != 2 || got.Model != "ollama:llama3.2" || got.Source != "model" || got.CreatedBy != "schedule" {
			t.Fatalf("fields lost: %+v", got)
		}
		if len(got.Groups) != 1 || got.Groups[0].Key != "github/acme/web" || len(got.Groups[0].EventIDs) != 2 || len(got.Highlights) != 1 || got.Highlights[0].Score != 75 {
			t.Fatalf("nested data lost: %+v", got)
		}
		if !got.To.Equal(contractNow.Add(time.Hour)) {
			t.Fatalf("window lost: %v", got.To)
		}
	})

	t.Run("the list is newest first and limited", func(t *testing.T) {
		list, err := s.List(ctx, 10)
		if err != nil || len(list) != 3 || list[0].ID != "d3" || list[2].ID != "d1" {
			t.Fatalf("list = %+v %v", list, err)
		}
		if one, _ := s.List(ctx, 1); len(one) != 1 || one[0].ID != "d3" {
			t.Fatalf("limit = %+v", one)
		}
	})

	t.Run("personal digests are kept apart from the workspace's", func(t *testing.T) {
		for i, id := range []string{"a1", "a2"} {
			at := contractNow.Add(time.Duration(10+i) * time.Hour)
			if err := s.Add(ctx, Digest{ID: id, UserID: "alice", From: at.Add(-time.Hour), To: at, Count: 1, Title: "mine " + id, CreatedBy: "alice@corp.com", CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add(ctx, Digest{ID: "b1", UserID: "bob", To: contractNow, Title: "his", CreatedAt: contractNow.Add(20 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		work, _ := s.List(ctx, 50)
		for _, d := range work {
			if d.UserID != "" {
				t.Fatalf("the workspace list must not include a personal digest: %+v", d)
			}
		}
		if len(work) != 3 {
			t.Fatalf("workspace digests = %d, want 3", len(work))
		}
		mine, err := s.ListFor(ctx, "alice", 50)
		if err != nil || len(mine) != 2 || mine[0].ID != "a2" || mine[1].ID != "a1" || mine[0].UserID != "alice" {
			t.Fatalf("alice's digests = %+v %v", mine, err)
		}
		if one, _ := s.ListFor(ctx, "alice", 1); len(one) != 1 || one[0].ID != "a2" {
			t.Fatalf("limit = %+v", one)
		}
		if none, _ := s.ListFor(ctx, "carol", 50); len(none) != 0 {
			t.Fatalf("carol has none: %+v", none)
		}
		if _, err := s.ListFor(ctx, "", 5); err == nil {
			t.Fatal("a person is required")
		}
		if got, err := s.Get(ctx, "b1"); err != nil || got.UserID != "bob" {
			t.Fatalf("Get must find a personal digest by id: %+v %v", got, err)
		}
	})
}

func TestMemoryDigestStoreContract(t *testing.T) { digestStoreContract(t, NewMemoryDigestStore()) }
