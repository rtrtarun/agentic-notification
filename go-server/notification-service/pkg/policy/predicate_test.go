package policy

import (
	"strings"
	"testing"

	"github.com/example/notification-service/pkg/event"
)

func evtWith(payload map[string]any) event.Event {
	return event.Event{ID: "e", Source: "agent", EventType: "tool.call.failed", Repository: "acme/api", Payload: payload}
}

func TestPredicateOperators(t *testing.T) {
	payload := map[string]any{
		"severity": "High",
		"draft":    true,
		"count":    "7",
		"metadata": map[string]any{"attempt": float64(4), "confidence": 0.62, "error": "timeout", "tool": "search"},
	}
	cases := []struct {
		name string
		p    Predicate
		want bool
	}{
		{"eq string is case-insensitive", Predicate{Field: "severity", Value: "high"}, true},
		{"eq string differs", Predicate{Field: "severity", Value: "low"}, false},
		{"eq number", Predicate{Field: "metadata.attempt", Op: OpEq, Value: 4}, true},
		{"eq bool", Predicate{Field: "draft", Value: true}, true},
		{"eq bool differs", Predicate{Field: "draft", Value: false}, false},
		{"ne", Predicate{Field: "metadata.tool", Op: OpNe, Value: "browser"}, true},
		{"ne equal", Predicate{Field: "metadata.tool", Op: OpNe, Value: "search"}, false},
		{"gt", Predicate{Field: "metadata.attempt", Op: OpGt, Value: 3}, true},
		{"gt equal is false", Predicate{Field: "metadata.attempt", Op: OpGt, Value: 4}, false},
		{"gte equal", Predicate{Field: "metadata.attempt", Op: OpGte, Value: 4}, true},
		{"lt float", Predicate{Field: "metadata.confidence", Op: OpLt, Value: 0.7}, true},
		{"lt false", Predicate{Field: "metadata.confidence", Op: OpLt, Value: 0.5}, false},
		{"lte", Predicate{Field: "metadata.confidence", Op: OpLte, Value: 0.62}, true},
		{"a numeric string counts as a number", Predicate{Field: "count", Op: OpGt, Value: 5}, true},
		{"exists", Predicate{Field: "metadata.error", Op: OpExists}, true},
		{"exists true explicit", Predicate{Field: "metadata.error", Op: OpExists, Value: true}, true},
		{"exists on a missing field", Predicate{Field: "metadata.nothing", Op: OpExists}, false},
		{"not exists", Predicate{Field: "metadata.nothing", Op: OpExists, Value: false}, true},
		{"not exists but present", Predicate{Field: "metadata.error", Op: OpExists, Value: false}, false},
		{"a missing field never matches eq", Predicate{Field: "nope", Value: "x"}, false},
		{"a missing field never matches ne", Predicate{Field: "nope", Op: OpNe, Value: "x"}, false},
		{"a missing field never matches gt", Predicate{Field: "nope", Op: OpGt, Value: 1}, false},
		{"a non-number never matches gt", Predicate{Field: "metadata.tool", Op: OpGt, Value: 1}, false},
		{"a path through a non-object never matches", Predicate{Field: "severity.level", Value: "x"}, false},
		{"unknown operator never matches", Predicate{Field: "severity", Op: "like", Value: "h"}, false},
	}
	for _, c := range cases {
		if got := c.p.matches(evtWith(payload)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNormalisedFieldNames(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		field   string
		want    any
		found   bool
	}{
		{"branch from a push ref", map[string]any{"ref": "refs/heads/main"}, "branch", "main", true},
		{"branch from a pull request", map[string]any{"pull_request": map[string]any{"head": map[string]any{"ref": "feature/x"}}}, "branch", "feature/x", true},
		{"branch given directly wins", map[string]any{"branch": "release", "ref": "refs/heads/main"}, "branch", "release", true},
		{"no branch", map[string]any{}, "branch", nil, false},
		{"author from a sender", map[string]any{"sender": map[string]any{"login": "alice"}}, "author", "alice", true},
		{"author from a pusher", map[string]any{"pusher": map[string]any{"name": "bob"}}, "author", "bob", true},
		{"author from metadata", map[string]any{"metadata": map[string]any{"author": "agent-7"}}, "author", "agent-7", true},
		{"severity from an advisory", map[string]any{"security_advisory": map[string]any{"severity": "critical"}}, "severity", "critical", true},
		{"severity from metadata", map[string]any{"metadata": map[string]any{"severity": "low"}}, "severity", "low", true},
		{"no severity", map[string]any{}, "severity", nil, false},
	}
	for _, c := range cases {
		got, found := Resolve(evtWith(c.payload), c.field)
		if found != c.found || got != c.want {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, found, c.want, c.found)
		}
	}
}

func TestAllPredicatesMustHoldAndAreCombinedWithTheOtherFields(t *testing.T) {
	cond := RuleCondition{
		Source: "agent",
		PayloadMatch: []Predicate{
			{Field: "metadata.attempt", Op: OpGt, Value: 3},
			{Field: "metadata.confidence", Op: OpLt, Value: 0.7},
		},
	}
	hit := evtWith(map[string]any{"metadata": map[string]any{"attempt": 5, "confidence": 0.5}})
	if !Matches(cond, hit) {
		t.Fatal("both predicates hold, so the rule should match")
	}
	if Matches(cond, evtWith(map[string]any{"metadata": map[string]any{"attempt": 5, "confidence": 0.9}})) {
		t.Fatal("one failing predicate must stop the match")
	}
	other := hit
	other.Source = "github"
	if Matches(cond, other) {
		t.Fatal("the source must still match")
	}
	if !Matches(RuleCondition{Source: "agent"}, evtWith(nil)) {
		t.Fatal("a rule without predicates is unchanged")
	}
}

func TestValidatePredicates(t *testing.T) {
	good := []Predicate{
		{Field: "metadata.attempt", Op: OpGt, Value: 3},
		{Field: "severity", Value: "high"},
		{Field: "metadata.error", Op: OpExists},
		{Field: "metadata.error", Op: OpExists, Value: false},
		{Field: "draft", Op: OpNe, Value: true},
	}
	if err := ValidatePredicates(good); err != nil {
		t.Fatalf("valid predicates refused: %v", err)
	}
	bad := map[string][]Predicate{
		"empty field":             {{Field: "", Value: "x"}},
		"bad characters":          {{Field: "a b", Value: "x"}},
		"starts with a dot":       {{Field: ".a", Value: "x"}},
		"unknown operator":        {{Field: "a", Op: "like", Value: "x"}},
		"eq with no value":        {{Field: "a"}},
		"eq with an object":       {{Field: "a", Value: map[string]any{"x": 1}}},
		"gt with text":            {{Field: "a", Op: OpGt, Value: "many"}},
		"gt with no value":        {{Field: "a", Op: OpGt}},
		"exists with text":        {{Field: "a", Op: OpExists, Value: "yes"}},
		"a very long value":       {{Field: "a", Value: strings.Repeat("x", 300)}},
		"a very long field":       {{Field: strings.Repeat("a", 101), Value: "x"}},
		"too many predicates":     {{Field: "a", Value: 1}, {Field: "b", Value: 1}, {Field: "c", Value: 1}, {Field: "d", Value: 1}, {Field: "e", Value: 1}, {Field: "f", Value: 1}},
		"a numeric string for gt": {{Field: "a", Op: OpGt, Value: "abc"}},
	}
	for name, preds := range bad {
		if err := ValidatePredicates(preds); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := ValidatePredicates([]Predicate{{Field: "a", Op: OpGt, Value: "5"}}); err != nil {
		t.Errorf("a numeric string is a usable threshold: %v", err)
	}
}

func TestValidateChannels(t *testing.T) {
	if err := ValidateChannels("standard", []string{"slack", "email"}); err != nil {
		t.Fatalf("valid channels refused: %v", err)
	}
	if err := ValidateChannels("background", nil); err != nil {
		t.Fatalf("no channels is fine: %v", err)
	}
	for name, c := range map[string]struct {
		class    string
		channels []string
	}{
		"critical rule": {"critical", []string{"slack"}},
		"unknown":       {"standard", []string{"pager"}},
		"duplicate":     {"standard", []string{"slack", "slack"}},
		"wrong case":    {"standard", []string{"Slack"}},
	} {
		if err := ValidateChannels(c.class, c.channels); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := ValidateChannels("critical", nil); err != nil {
		t.Fatalf("a critical rule with no channels is normal: %v", err)
	}
}

func TestEvaluateReturnsChannelsExceptForCriticalRules(t *testing.T) {
	e := NewEngine([]Rule{
		{Name: "crit", When: RuleCondition{EventType: "outage"}, Then: RuleOutcome{AttentionClass: "critical", Reason: "r", Channels: []string{"slack"}}},
		{Name: "dedicated", When: RuleCondition{Source: "agent"}, Then: RuleOutcome{AttentionClass: "standard", Reason: "r", Channels: []string{"slack", "email"}}},
	})
	d, ok := e.Evaluate(event.Event{Source: "agent", EventType: "x"})
	if !ok || strings.Join(d.Channels, ",") != "slack,email" {
		t.Fatalf("decision = %+v", d)
	}
	d.Channels[0] = "tampered"
	if again, _ := e.Evaluate(event.Event{Source: "agent", EventType: "x"}); again.Channels[0] != "slack" {
		t.Fatal("a caller must not be able to change a rule's channels")
	}
	crit, _ := e.Evaluate(event.Event{Source: "agent", EventType: "outage"})
	if crit.AttentionClass != "critical" || len(crit.Channels) != 0 {
		t.Fatalf("a critical rule must never narrow its channels: %+v", crit)
	}
}
