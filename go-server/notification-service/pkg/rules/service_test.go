package rules_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/policy"
	"github.com/example/notification-service/pkg/rules"
)

func seed() []policy.Rule {
	return []policy.Rule{
		{Name: "security", Locked: true, When: policy.RuleCondition{Source: "github", EventType: "security_advisory"},
			Then: policy.RuleOutcome{AttentionClass: "critical", Reason: "security"}},
		{Name: "default-github", When: policy.RuleCondition{Source: "github"},
			Then: policy.RuleOutcome{AttentionClass: "standard", Reason: "default"}},
	}
}

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%d", n) }
}

func newService(t *testing.T) (*rules.Service, *policy.Engine, *rules.MemoryStore) {
	t.Helper()
	store := rules.NewMemoryStore()
	engine := policy.NewEngine(nil)
	svc, err := rules.NewService(context.Background(), store, engine, seed())
	if err != nil {
		t.Fatal(err)
	}
	return svc.WithIDGenerator(counter()), engine, store
}

func names(list []policy.Rule) []string {
	var out []string
	for _, r := range list {
		out = append(out, r.Name)
	}
	return out
}

func TestSeedsEmptyStoreAndAppliesToEngine(t *testing.T) {
	svc, engine, _ := newService(t)
	list, _ := svc.List(context.Background())
	if len(list) != 2 || list[0].ID == "" || list[1].ID == "" || list[0].ID == list[1].ID {
		t.Fatalf("seeded = %+v", list)
	}
	d, ok := engine.Evaluate(event.Event{Source: "github", EventType: "security_advisory"})
	if !ok || d.AttentionClass != "critical" {
		t.Fatalf("engine not loaded: %+v %v", d, ok)
	}
}

func TestStoredRulesWinOverSeedButMissingLockedRulesAreAdded(t *testing.T) {
	store := rules.NewMemoryStore()
	_ = store.Replace(context.Background(), []policy.Rule{{ID: "a", Name: "mine", Then: policy.RuleOutcome{AttentionClass: "standard"}}})
	svc, err := rules.NewService(context.Background(), store, policy.NewEngine(nil), seed())
	if err != nil {
		t.Fatal(err)
	}
	list, _ := svc.List(context.Background())
	if got := names(list); len(got) != 2 || got[0] != "security" || got[1] != "mine" {
		t.Fatalf("order = %v, want locked seed first then stored rule (no default-github re-added)", got)
	}
}

func TestCreateAppendsAndNewRuleTakesEffect(t *testing.T) {
	svc, engine, _ := newService(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, policy.Rule{Name: " noisy-bot ", When: policy.RuleCondition{Repository: "acme/*"},
		Then: policy.RuleOutcome{AttentionClass: "background"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "noisy-bot" || created.Then.Reason != "matched rule noisy-bot" || created.Locked {
		t.Fatalf("created = %+v", created)
	}
	list, _ := svc.List(ctx)
	if got := names(list); got[len(got)-1] != "noisy-bot" {
		t.Fatalf("order = %v", got)
	}
	// "default-github" sits above the new rule, so it still wins for github events.
	d, _ := engine.Evaluate(event.Event{Source: "github", Repository: "acme/api"})
	if d.MatchedRule != "default-github" {
		t.Fatalf("matched %q", d.MatchedRule)
	}
	d, _ = engine.Evaluate(event.Event{Source: "gitlab", Repository: "acme/api"})
	if d.MatchedRule != "noisy-bot" {
		t.Fatalf("matched %q", d.MatchedRule)
	}
}

func TestCreateCannotForgeALockedRule(t *testing.T) {
	svc, _, _ := newService(t)
	r, err := svc.Create(context.Background(), policy.Rule{Name: "sneaky", Locked: true, Then: policy.RuleOutcome{AttentionClass: "standard"}})
	if err != nil || r.Locked {
		t.Fatalf("locked flag from the API must be ignored: %+v %v", r, err)
	}
}

func TestValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	bad := map[string]policy.Rule{
		"no name":     {Then: policy.RuleOutcome{AttentionClass: "standard"}},
		"duplicate":   {Name: "DEFAULT-GITHUB", Then: policy.RuleOutcome{AttentionClass: "standard"}},
		"typo class":  {Name: "x", Then: policy.RuleOutcome{AttentionClass: "critcal"}},
		"bad pattern": {Name: "y", When: policy.RuleCondition{Repository: "acme/[x"}, Then: policy.RuleOutcome{AttentionClass: "standard"}},
		"long name":   {Name: string(make([]byte, 81)), Then: policy.RuleOutcome{AttentionClass: "standard"}},
		"long reason": {Name: "z", Then: policy.RuleOutcome{AttentionClass: "standard", Reason: string(make([]rune, 201))}},
	}
	for name, r := range bad {
		if _, err := svc.Create(ctx, r); !errors.Is(err, rules.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestLockedRulesCannotBeChanged(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	list, _ := svc.List(ctx)
	locked := list[0]
	if _, err := svc.Update(ctx, locked.ID, policy.Rule{Name: "security", Then: policy.RuleOutcome{AttentionClass: "background"}}); !errors.Is(err, rules.ErrLocked) {
		t.Fatalf("update err = %v", err)
	}
	if err := svc.Delete(ctx, locked.ID); !errors.Is(err, rules.ErrLocked) {
		t.Fatalf("delete err = %v", err)
	}
}

func TestUpdateKeepsIDAndPositionAndAllowsOwnName(t *testing.T) {
	svc, engine, _ := newService(t)
	ctx := context.Background()
	list, _ := svc.List(ctx)
	target := list[1]
	updated, err := svc.Update(ctx, target.ID, policy.Rule{Name: "default-github", Disabled: true,
		When: policy.RuleCondition{Source: "github"}, Then: policy.RuleOutcome{AttentionClass: "background", Reason: "quiet"}})
	if err != nil || updated.ID != target.ID {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	// Disabled rules never match, so unmatched events fall back to standard.
	if d, ok := engine.Evaluate(event.Event{Source: "github"}); ok {
		t.Fatalf("disabled rule matched: %+v", d)
	}
	if _, err := svc.Update(ctx, "missing", policy.Rule{Name: "q", Then: policy.RuleOutcome{AttentionClass: "standard"}}); !errors.Is(err, rules.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestDelete(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	list, _ := svc.List(ctx)
	if err := svc.Delete(ctx, list[1].ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := svc.List(ctx); len(after) != 1 {
		t.Fatalf("after delete = %v", names(after))
	}
	if err := svc.Delete(ctx, list[1].ID); !errors.Is(err, rules.ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestReorder(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	a, _ := svc.Create(ctx, policy.Rule{Name: "a", Then: policy.RuleOutcome{AttentionClass: "standard"}})
	b, _ := svc.Create(ctx, policy.Rule{Name: "b", Then: policy.RuleOutcome{AttentionClass: "standard"}})
	list, _ := svc.List(ctx)
	sec, def := list[0].ID, list[1].ID

	if err := svc.Reorder(ctx, []string{sec, b.ID, a.ID, def}); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.List(ctx)
	if got := names(after); fmt.Sprint(got) != "[security b a default-github]" {
		t.Fatalf("order = %v", got)
	}

	for name, ids := range map[string][]string{
		"locked below unlocked": {b.ID, sec, a.ID, def},
		"missing id":            {sec, b.ID, a.ID},
		"repeat":                {sec, sec, a.ID, def},
		"unknown":               {sec, b.ID, a.ID, "nope"},
	} {
		if err := svc.Reorder(ctx, ids); !errors.Is(err, rules.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMaxRules(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	for i := 0; ; i++ {
		_, err := svc.Create(ctx, policy.Rule{Name: fmt.Sprintf("r%d", i), Then: policy.RuleOutcome{AttentionClass: "standard"}})
		if err != nil {
			if !errors.Is(err, rules.ErrInvalid) || i+2 != rules.MaxRules {
				t.Fatalf("stopped at %d with %v", i, err)
			}
			return
		}
	}
}

func TestEvaluateTraceAndBypass(t *testing.T) {
	svc, _, _ := newService(t)
	res, err := svc.Test(context.Background(), rules.Sample{Source: "github", EventType: "security_advisory", Repository: "acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Matched || res.MatchedRule != "security" || !res.Bypassed || res.AttentionClass != "critical" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Steps) != 2 || !res.Steps[0].Winner || !res.Steps[1].Matched || res.Steps[1].Winner {
		t.Fatalf("steps = %+v", res.Steps)
	}
	none, _ := svc.Test(context.Background(), rules.Sample{Source: "jira"})
	if none.Matched || none.AttentionClass != "standard" || none.Bypassed {
		t.Fatalf("unmatched = %+v", none)
	}
}

func TestEvaluateWildcards(t *testing.T) {
	list := []policy.Rule{{ID: "1", Name: "acme", When: policy.RuleCondition{Repository: "acme/*"}, Then: policy.RuleOutcome{AttentionClass: "background"}}}
	if !rules.Evaluate(list, rules.Sample{Repository: "acme/api"}).Matched {
		t.Fatal("acme/* should match acme/api")
	}
	if rules.Evaluate(list, rules.Sample{Repository: "acme/api/v2"}).Matched {
		t.Fatal("* must not cross a slash")
	}
	if rules.Evaluate(list, rules.Sample{Repository: "other/api"}).Matched {
		t.Fatal("other org matched")
	}
}
