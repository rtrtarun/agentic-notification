package attention

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/example/notification-service/pkg/event"
)

// Heuristic is the built-in, offline implementation of Classifier, Summarizer
// and Explainer. It is deterministic: the same event always gets the same
// score, and every point of it comes with a reason a person can read.
type Heuristic struct{}

// NewHeuristic returns the built-in implementation.
func NewHeuristic() *Heuristic { return &Heuristic{} }

const (
	baseScore       = 50
	backgroundBelow = 35
)

// Event types that are activity rather than work.
var socialTypes = map[string]bool{"star": true, "watch": true, "fork": true, "public": true, "member": true, "gollum": true}

// Actions that change a thing without asking anyone to do anything.
var chatterActions = map[string]bool{
	"labeled": true, "unlabeled": true, "assigned": true, "unassigned": true, "edited": true,
	"milestoned": true, "demilestoned": true, "synchronize": true, "converted_to_draft": true,
}

// Classify scores an event from what it is and who sent it. Below 35 it is
// background; otherwise it stays standard. It never answers critical.
func (h *Heuristic) Classify(ctx context.Context, evt event.Event) (Classification, error) {
	if err := ctx.Err(); err != nil {
		return Classification{}, err
	}
	if evt.AttentionClass == event.AttentionCritical {
		return Classification{}, ErrCritical
	}
	score := baseScore
	var reasons []string
	add := func(delta int, why string) {
		score += delta
		reasons = append(reasons, why)
	}

	typ := strings.ToLower(evt.EventType)
	action := strings.ToLower(str(evt.Payload, "action"))

	if socialTypes[typ] {
		add(-35, "social activity")
	}
	if chatterActions[action] {
		add(-20, "routine change ("+action+")")
	}
	if isBot(evt.Payload) {
		add(-25, "automated sender")
	}
	if pr, ok := evt.Payload["pull_request"].(map[string]any); ok {
		if draft, _ := pr["draft"].(bool); draft {
			add(-20, "draft pull request")
		}
	}

	switch {
	case action == "review_requested":
		add(25, "someone asked for your review")
	case typ == "pull_request_review" && strings.EqualFold(str(evt.Payload, "review", "state"), "changes_requested"):
		add(20, "changes were requested")
	case (typ == "check_run" || typ == "workflow_run" || typ == "check_suite") && isFailure(evt.Payload):
		add(30, "a check failed")
	case (typ == "pull_request" || typ == "issues") && action == "opened":
		add(10, "something new was opened")
	case typ == "issue_comment" || typ == "pull_request_review_comment":
		add(10, "a person commented")
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	class, confidence := event.AttentionStandard, 0.6
	if score < backgroundBelow {
		class = event.AttentionBackground
		confidence = clamp(0.55+float64(backgroundBelow-score)/100, 0.55, 0.95)
	} else if score > baseScore {
		confidence = clamp(0.6+float64(score-baseScore)/100, 0.6, 0.95)
	}
	return Classification{Class: class, Score: score, Confidence: confidence, Reasons: reasons, Model: ModelHeuristic}, nil
}

// Summarize groups events by source and repository and describes each group.
func (h *Heuristic) Summarize(ctx context.Context, events []event.Event) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	byKey := map[string]*Group{}
	types := map[string]map[string]int{}
	sources := map[string]int{}
	for _, e := range events {
		if e.AttentionClass == event.AttentionCritical {
			continue // a critical event is never summarised; it was sent at once
		}
		key := e.Source
		title := e.Source
		if e.Repository != "" {
			key, title = e.Source+"/"+e.Repository, e.Repository+" ("+e.Source+")"
		}
		g := byKey[key]
		if g == nil {
			g = &Group{Key: key, Title: title}
			byKey[key], types[key] = g, map[string]int{}
		}
		g.Count++
		g.EventIDs = append(g.EventIDs, e.ID)
		types[key][e.EventType]++
		sources[e.Source]++
	}
	groups := make([]Group, 0, len(byKey))
	total := 0
	for key, g := range byKey {
		g.Summary = fmt.Sprintf("%s: %s", plural(g.Count, "notification"), counts(types[key], 3))
		groups = append(groups, *g)
		total += g.Count
	}
	sort.Slice(groups, func(a, b int) bool {
		if groups[a].Count != groups[b].Count {
			return groups[a].Count > groups[b].Count
		}
		return groups[a].Key < groups[b].Key
	})
	if total == 0 {
		return Summary{Title: "Nothing held back", Text: "Nothing was held back.", Groups: groups}, nil
	}
	text := fmt.Sprintf("%s held back, from %s.", plural(total, "notification"), counts(sources, 3))
	if len(groups) > 0 {
		text += fmt.Sprintf(" Busiest: %s with %d.", groups[0].Title, groups[0].Count)
	}
	return Summary{Title: fmt.Sprintf("%s held back", plural(total, "notification")), Text: text, Groups: groups}, nil
}

// Explain says in a few sentences why the event was handled the way it was.
func (h *Heuristic) Explain(ctx context.Context, evt event.Event) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	state := evt.DecisionState
	if state == nil {
		return "No decision has been recorded for this event.", nil
	}
	var parts []string
	switch {
	case evt.AttentionClass == event.AttentionCritical:
		parts = append(parts, "This is critical, so it was sent to every channel at once. No judgement was involved and nothing can mute it.")
		parts = append(parts, ruleSentence(state))
	case state.Status == event.DecisionMerged:
		parts = append(parts, "This repeated an earlier notification, so it was merged into it instead of interrupting you again.")
		if evt.MergedInto != "" {
			parts = append(parts, "The first one is "+evt.MergedInto+".")
		}
	case state.Status == event.DecisionDigest:
		parts = append(parts, "It was held for your digest instead of being sent now.")
		parts = append(parts, ruleSentence(state))
	case state.Status == event.DecisionDelayed:
		parts = append(parts, "It was held until quiet hours end.")
		parts = append(parts, ruleSentence(state))
	default:
		parts = append(parts, "It was kept as a "+evt.AttentionClass+" notification and queued.")
		parts = append(parts, ruleSentence(state))
	}
	if state.ModelVersion != "" {
		parts = append(parts, fmt.Sprintf("Attention Intelligence (%s) refined this decision with %.0f%% confidence.", state.ModelVersion, state.Confidence*100))
	} else if evt.AttentionClass != event.AttentionCritical {
		parts = append(parts, "A rule or a choice decided this; Attention Intelligence was not used.")
	}
	return strings.Join(parts, " "), nil
}

func ruleSentence(s *event.DecisionState) string {
	switch {
	case s.MatchedRule != "" && s.Reason != "":
		return fmt.Sprintf("The rule %q applied: %s.", s.MatchedRule, strings.TrimRight(s.Reason, "."))
	case s.Reason != "":
		return strings.TrimRight(s.Reason, ".") + "."
	}
	return "No rule matched, so the default applied."
}

// ---- helpers ----

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = next[p]
	}
	s, _ := cur.(string)
	return s
}

func isBot(payload map[string]any) bool {
	if strings.EqualFold(str(payload, "sender", "type"), "bot") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(str(payload, "sender", "login")), "[bot]")
}

func isFailure(payload map[string]any) bool {
	for _, key := range []string{"check_run", "workflow_run", "check_suite"} {
		if c := strings.ToLower(str(payload, key, "conclusion")); c == "failure" || c == "timed_out" || c == "cancelled" {
			return true
		}
	}
	return false
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// counts renders "3 pull_request, 2 push" for the biggest entries, with "and N more kinds" when cut.
func counts(m map[string]int, top int) string {
	type kv struct {
		k string
		n int
	}
	list := make([]kv, 0, len(m))
	for k, n := range m {
		if k == "" {
			k = "other"
		}
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].n != list[b].n {
			return list[a].n > list[b].n
		}
		return list[a].k < list[b].k
	})
	var out []string
	for i, e := range list {
		if i == top {
			out = append(out, fmt.Sprintf("and %d more", len(list)-top))
			break
		}
		out = append(out, fmt.Sprintf("%d %s", e.n, e.k))
	}
	return strings.Join(out, ", ")
}
