package attention

import (
	"context"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

type countingNotifier struct{ n int }

func (c *countingNotifier) Notify(context.Context, string, string, string) error {
	c.n++
	return nil
}

func personalEvents() []event.Event {
	crit := held("crit", "security_advisory", t0.Add(time.Hour), event.DecisionDigest, nil)
	crit.AttentionClass = event.AttentionCritical
	return []event.Event{
		held("keep", "pull_request", t0.Add(time.Hour), event.DecisionDigest, nil),
		held("star1", "star", t0.Add(2*time.Hour), event.DecisionDigest, nil),
		held("over", "issues", t0.Add(3*time.Hour), event.DecisionBudgetExceeded, nil),
		crit,
	}
}

func dropsStars(e event.Event) bool { return e.EventType == "star" }

func TestAPersonalDigestOmitsWhatThePersonDoesNotWantAndNeverIncludesCritical(t *testing.T) {
	g, store := newGen(personalEvents(), nil)
	d, made, err := g.GenerateFor(ctx, "alice@corp.com", "alice", dropsStars)
	if err != nil || !made {
		t.Fatalf("made=%v err=%v", made, err)
	}
	if d.UserID != "alice" || d.Count != 2 || d.CreatedBy != "alice@corp.com" {
		t.Fatalf("digest = %+v", d)
	}
	for _, grp := range d.Groups {
		for _, id := range grp.EventIDs {
			if id == "star1" || id == "crit" {
				t.Fatalf("%s must not be in alice's digest", id)
			}
		}
	}
	if mine, _ := store.ListFor(ctx, "alice", 5); len(mine) != 1 {
		t.Fatalf("stored for alice = %+v", mine)
	}
	if work, _ := store.List(ctx, 5); len(work) != 0 {
		t.Fatalf("a personal digest must not appear in the workspace list: %+v", work)
	}
}

func TestPersonalAndWorkspaceDigestsHaveIndependentWindows(t *testing.T) {
	g, _ := newGen(personalEvents(), nil)
	if _, made, _ := g.GenerateFor(ctx, "alice", "alice", dropsStars); !made {
		t.Fatal("alice's first digest")
	}
	if _, made, _ := g.GenerateFor(ctx, "alice", "alice", dropsStars); made {
		t.Fatal("nothing new since alice's last digest")
	}
	w, made, err := g.Generate(ctx, "schedule")
	if err != nil || !made || w.Count != 3 || w.UserID != "" {
		t.Fatalf("the workspace digest still covers everything held: %+v %v %v", w, made, err)
	}
	if _, made, _ := g.GenerateFor(ctx, "bob", "bob", nil); !made {
		t.Fatal("bob has had no digest yet, so his window starts at the beginning")
	}
}

func TestPersonalDigestsAreNotDelivered(t *testing.T) {
	n := &countingNotifier{}
	g, _ := newGen(personalEvents(), n)
	if _, made, _ := g.GenerateFor(ctx, "alice", "alice", nil); !made {
		t.Fatal("expected a digest")
	}
	if n.n != 0 {
		t.Fatalf("a personal digest has no delivery target, but %d messages were queued", n.n)
	}
	if _, made, _ := g.Generate(ctx, "schedule"); !made || n.n != 1 {
		t.Fatalf("the workspace digest is still delivered: notified %d times", n.n)
	}
}

func TestAPersonalDigestNeedsAPerson(t *testing.T) {
	g, _ := newGen(personalEvents(), nil)
	if _, _, err := g.GenerateFor(ctx, "x", "", nil); err == nil {
		t.Fatal("an empty user must be refused")
	}
}
