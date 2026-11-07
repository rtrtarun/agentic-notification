package feedback

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/overrides"
)

var ctx = context.Background()

func fixtures(t *testing.T) (*Service, *overrides.Service, *MemoryStore) {
	t.Helper()
	events := map[string]event.Event{
		"std":  {ID: "std", Source: "github", EventType: "pull_request", Repository: "acme/web", AttentionClass: event.AttentionStandard},
		"crit": {ID: "crit", Source: "github", EventType: "security_advisory", Repository: "acme/web", AttentionClass: event.AttentionCritical},
	}
	lookup := func(_ context.Context, id string) (event.Event, error) {
		if e, ok := events[id]; ok {
			return e, nil
		}
		return event.Event{}, ErrEventNotFound
	}
	ov, err := overrides.NewService(ctx, overrides.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	return NewService(store, lookup, ov), ov, store
}

func TestSignalsAreStoredWithTheEventContext(t *testing.T) {
	svc, _, store := fixtures(t)
	for _, a := range []string{ActionOpened, ActionDismissed} {
		if _, err := svc.Record(ctx, "ann@corp.com", Input{EventID: "std", Action: a}, true); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	if _, err := svc.Record(ctx, "ann@corp.com", Input{EventID: "std", Action: ActionReply, Reason: "  too noisy  "}, true); err != nil {
		t.Fatal(err)
	}
	got, _ := store.List(ctx, Filter{})
	if len(got) != 3 || got[0].Action != ActionReply || got[0].Reason != "too noisy" {
		t.Fatalf("stored = %+v", got)
	}
	if got[0].UserID != "ann@corp.com" || got[0].Source != "github" || got[0].EventType != "pull_request" || got[0].Repository != "acme/web" || got[0].ID == "" || got[0].CreatedAt.IsZero() {
		t.Fatalf("feedback lacks attribution or context: %+v", got[0])
	}
}

func TestAlwaysAndNeverCreateOverridesByScope(t *testing.T) {
	svc, ov, _ := fixtures(t)
	for _, c := range []struct {
		scope string
		want  overrides.Override
	}{
		{"", overrides.Override{Source: "github", EventType: "pull_request"}},
		{ScopeType, overrides.Override{Source: "github", EventType: "pull_request"}},
		{ScopeRepository, overrides.Override{Repository: "acme/web"}},
		{ScopeSource, overrides.Override{Source: "github"}},
	} {
		res, err := svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionNever, Scope: c.scope}, true)
		if err != nil || res.Override == nil {
			t.Fatalf("scope %q: %v %+v", c.scope, err, res)
		}
		o := *res.Override
		if o.Source != c.want.Source || o.EventType != c.want.EventType || o.Repository != c.want.Repository || o.Mode != ModeNeverForTest || o.CreatedBy != "ann" {
			t.Errorf("scope %q: override = %+v", c.scope, o)
		}
		if res.Feedback.OverrideID != o.ID {
			t.Error("the feedback must remember which override it created")
		}
	}
	if len(ov.List()) != 3 {
		t.Fatalf("overrides = %d (the type scope is asked for twice and must collapse)", len(ov.List()))
	}
}

// ModeNeverForTest keeps the literal out of the table above.
const ModeNeverForTest = overrides.ModeNever

func TestNeverOnACriticalEventIsRefused(t *testing.T) {
	svc, ov, store := fixtures(t)
	if _, err := svc.Record(ctx, "ann", Input{EventID: "crit", Action: ActionNever}, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if len(ov.List()) != 0 {
		t.Fatal("no override may be created")
	}
	if got, _ := store.List(ctx, Filter{}); len(got) != 0 {
		t.Fatal("a refused request is not a signal")
	}
	if _, err := svc.Record(ctx, "ann", Input{EventID: "crit", Action: ActionAlways}, true); err != nil {
		t.Fatalf("always on a critical event is harmless and allowed: %v", err)
	}
}

func TestCallersWhoMayNotOverrideStillLeaveASignal(t *testing.T) {
	svc, ov, store := fixtures(t)
	res, err := svc.Record(ctx, "mo", Input{EventID: "std", Action: ActionNever}, false)
	if err != nil || !res.OverrideSkipped || res.Override != nil {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if len(ov.List()) != 0 {
		t.Fatal("no override")
	}
	if got, _ := store.List(ctx, Filter{}); len(got) != 1 || got[0].Action != ActionNever {
		t.Fatal("the signal must still be stored for learning")
	}
}

func TestUndoRevertsTheLatestOverrideOfThatPersonOnly(t *testing.T) {
	svc, ov, _ := fixtures(t)
	svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionNever}, true)
	svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionOpened}, true) // unrelated signal in between

	res, err := svc.Record(ctx, "bob", Input{EventID: "std", Action: ActionUndo}, true)
	if err != nil || res.Reverted || len(ov.List()) != 1 {
		t.Fatalf("someone else's undo must not remove ann's override: %+v %v", res, err)
	}
	res, err = svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionUndo}, true)
	if err != nil || !res.Reverted || len(ov.List()) != 0 {
		t.Fatalf("ann's undo must remove it: %+v %v", res, err)
	}
	res, _ = svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionUndo}, true)
	if res.Reverted {
		t.Fatal("undoing twice must not revert anything else")
	}
}

func TestUndoOfADismissalOnlyRecords(t *testing.T) {
	svc, _, store := fixtures(t)
	svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionDismissed}, true)
	res, err := svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionUndo}, true)
	if err != nil || res.Reverted {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if got, _ := store.List(ctx, Filter{}); len(got) != 2 {
		t.Fatalf("stored %d", len(got))
	}
}

func TestUndoToleratesAnOverrideRemovedMeanwhile(t *testing.T) {
	svc, ov, _ := fixtures(t)
	res, _ := svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionAlways}, true)
	ov.Delete(ctx, res.Override.ID)
	if _, err := svc.Record(ctx, "ann", Input{EventID: "std", Action: ActionUndo}, true); err != nil {
		t.Fatalf("an already-removed override is not an error: %v", err)
	}
}

func TestInputValidation(t *testing.T) {
	svc, _, _ := fixtures(t)
	long := strings.Repeat("x", MaxReasonLength+1)
	for name, in := range map[string]Input{
		"no action":               {EventID: "std"},
		"unknown action":          {EventID: "std", Action: "like"},
		"no event":                {Action: ActionOpened},
		"bad scope":               {EventID: "std", Action: ActionNever, Scope: "galaxy"},
		"scope on a dismissal":    {EventID: "std", Action: ActionDismissed, Scope: ScopeType},
		"empty reply":             {EventID: "std", Action: ActionReply, Reason: "   "},
		"reply too long":          {EventID: "std", Action: ActionReply, Reason: long},
		"note on dismiss too big": {EventID: "std", Action: ActionDismissed, Reason: long},
	} {
		if _, err := svc.Record(ctx, "ann", in, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := svc.Record(ctx, "ann", Input{EventID: "missing", Action: ActionOpened}, true); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("unknown event = %v", err)
	}
}

func TestWithoutOverridesFeedbackIsOnlyRecorded(t *testing.T) {
	lookup := func(context.Context, string) (event.Event, error) { return event.Event{ID: "e", Source: "s"}, nil }
	svc := NewService(NewMemoryStore(), lookup, nil)
	res, err := svc.Record(ctx, "a", Input{EventID: "e", Action: ActionNever}, true)
	if err != nil || !res.OverrideSkipped {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

func TestMemoryStoreFiltersAndBounds(t *testing.T) {
	st := NewMemoryStore()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	st.Add(ctx, EventFeedback{ID: "1", EventID: "a", UserID: "x", CreatedAt: base})
	st.Add(ctx, EventFeedback{ID: "2", EventID: "b", UserID: "y", CreatedAt: base.Add(time.Hour)})
	st.Add(ctx, EventFeedback{ID: "3", EventID: "a", UserID: "y", CreatedAt: base.Add(2 * time.Hour)})
	if got, _ := st.List(ctx, Filter{EventID: "a"}); len(got) != 2 || got[0].ID != "3" {
		t.Fatalf("by event = %+v", got)
	}
	if got, _ := st.List(ctx, Filter{UserID: "y"}); len(got) != 2 {
		t.Fatalf("by user = %+v", got)
	}
	if got, _ := st.List(ctx, Filter{Since: base.Add(90 * time.Minute)}); len(got) != 1 || got[0].ID != "3" {
		t.Fatalf("since = %+v", got)
	}
	if got, _ := st.List(ctx, Filter{Limit: 1}); len(got) != 1 {
		t.Fatalf("limit = %+v", got)
	}
	for i := 0; i < MaxInMemory+5; i++ {
		st.Add(ctx, EventFeedback{ID: "n", CreatedAt: base})
	}
	if got, _ := st.List(ctx, Filter{}); len(got) != MaxInMemory {
		t.Fatalf("kept %d", len(got))
	}
}
