package attention

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
)

var ctx = context.Background()

func ev(id, source, typ, repo, class string, payload map[string]any) event.Event {
	return event.Event{ID: id, Source: source, EventType: typ, Repository: repo, AttentionClass: class, Payload: payload}
}

func TestHeuristicClassification(t *testing.T) {
	h := NewHeuristic()
	cases := []struct {
		name      string
		e         event.Event
		wantClass string
		reason    string
	}{
		{"a star is social", ev("1", "github", "star", "a/b", "standard", nil), "background", "social activity"},
		{"a label change is routine", ev("2", "github", "pull_request", "a/b", "standard", map[string]any{"action": "labeled"}), "background", "routine change"},
		{"a bot alone is background", ev("3", "github", "push", "a/b", "standard", map[string]any{"sender": map[string]any{"login": "dependabot[bot]"}}), "background", "automated sender"},
		{"a bot asking for review stays", ev("3b", "github", "pull_request", "a/b", "standard", map[string]any{"action": "review_requested", "sender": map[string]any{"type": "Bot"}}), "standard", "asked for your review"},
		{"a bot labelling is quiet", ev("4", "github", "pull_request", "a/b", "standard", map[string]any{"action": "labeled", "sender": map[string]any{"type": "Bot"}}), "background", "automated sender"},
		{"a draft is quiet", ev("5", "github", "pull_request", "a/b", "standard", map[string]any{"action": "synchronize", "pull_request": map[string]any{"draft": true}}), "background", "draft pull request"},
		{"a review request stays", ev("6", "github", "pull_request", "a/b", "standard", map[string]any{"action": "review_requested"}), "standard", "asked for your review"},
		{"requested changes stay", ev("7", "github", "pull_request_review", "a/b", "standard", map[string]any{"review": map[string]any{"state": "changes_requested"}}), "standard", "changes were requested"},
		{"a failed check stays", ev("8", "github", "check_run", "a/b", "standard", map[string]any{"check_run": map[string]any{"conclusion": "failure"}}), "standard", "a check failed"},
		{"a plain event stays standard", ev("9", "jira", "issue", "", "standard", nil), "standard", ""},
	}
	for _, c := range cases {
		got, err := h.Classify(ctx, c.e)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.Class != c.wantClass || got.Model != ModelHeuristic {
			t.Errorf("%s: class %s model %s (score %d, %v)", c.name, got.Class, got.Model, got.Score, got.Reasons)
		}
		if !strings.Contains(got.Reason(), c.reason) {
			t.Errorf("%s: reason %q lacks %q", c.name, got.Reason(), c.reason)
		}
		if got.Score < 0 || got.Score > 100 || got.Confidence < 0.5 || got.Confidence > 1 {
			t.Errorf("%s: score %d confidence %f out of range", c.name, got.Score, got.Confidence)
		}
	}
}

func TestClassificationIsDeterministic(t *testing.T) {
	h := NewHeuristic()
	e := ev("1", "github", "pull_request", "a/b", "standard", map[string]any{"action": "labeled", "sender": map[string]any{"login": "x[bot]"}})
	first, _ := h.Classify(ctx, e)
	for i := 0; i < 20; i++ {
		again, _ := h.Classify(ctx, e)
		if fmt.Sprint(again) != fmt.Sprint(first) {
			t.Fatalf("run %d differs: %+v vs %+v", i, again, first)
		}
	}
}

func TestACriticalEventIsRefusedEverywhere(t *testing.T) {
	crit := ev("c", "github", "security_advisory", "a/b", event.AttentionCritical, nil)
	h := NewHeuristic()
	if _, err := h.Classify(ctx, crit); !errors.Is(err, ErrCritical) {
		t.Fatalf("heuristic: %v", err)
	}
	if _, err := Guarded(h).Classify(ctx, crit); !errors.Is(err, ErrCritical) {
		t.Fatalf("guard: %v", err)
	}
	s, _ := h.Summarize(ctx, []event.Event{crit})
	if len(s.Groups) != 0 {
		t.Fatalf("a critical event must never be summarised: %+v", s)
	}
}

type fixedClassifier struct {
	out   Classification
	calls int
}

func (f *fixedClassifier) Classify(context.Context, event.Event) (Classification, error) {
	f.calls++
	return f.out, nil
}

func TestGuardRejectsEscalationAndNonsense(t *testing.T) {
	for name, out := range map[string]Classification{
		"escalate to critical": {Class: event.AttentionCritical, Score: 99, Confidence: 1},
		"unknown class":        {Class: "urgent", Score: 50, Confidence: 1},
		"score too high":       {Class: event.AttentionStandard, Score: 101, Confidence: 1},
		"negative score":       {Class: event.AttentionStandard, Score: -1, Confidence: 1},
		"confidence above one": {Class: event.AttentionStandard, Score: 50, Confidence: 1.5},
	} {
		if _, err := Guarded(&fixedClassifier{out: out}).Classify(ctx, ev("1", "s", "t", "", "standard", nil)); err == nil {
			t.Errorf("%s: a model answer like this must be rejected", name)
		}
	}
	good := &fixedClassifier{out: Classification{Class: event.AttentionBackground, Score: 10, Confidence: 0.9}}
	if got, err := Guarded(good).Classify(ctx, ev("1", "s", "t", "", "standard", nil)); err != nil || got.Class != event.AttentionBackground {
		t.Fatalf("a valid answer must pass: %v", err)
	}
	if Guarded(nil) != nil {
		t.Fatal("guarding nothing stays nothing")
	}
	spy := &fixedClassifier{}
	Guarded(spy).Classify(ctx, ev("c", "s", "t", "", event.AttentionCritical, nil))
	if spy.calls != 0 {
		t.Fatal("the wrapped classifier must never see a critical event")
	}
}

func TestClassifierHonoursACancelledContext(t *testing.T) {
	c, cancel := context.WithCancel(ctx)
	cancel()
	h := NewHeuristic()
	if _, err := h.Classify(c, ev("1", "s", "t", "", "standard", nil)); err == nil {
		t.Fatal("classify")
	}
	if _, err := h.Summarize(c, nil); err == nil {
		t.Fatal("summarize")
	}
	if _, err := h.Explain(c, ev("1", "s", "t", "", "standard", nil)); err == nil {
		t.Fatal("explain")
	}
}

func TestSummarizeGroupsAndOrders(t *testing.T) {
	h := NewHeuristic()
	events := []event.Event{
		ev("1", "github", "pull_request", "acme/web", "standard", nil),
		ev("2", "github", "pull_request", "acme/web", "standard", nil),
		ev("3", "github", "push", "acme/web", "standard", nil),
		ev("4", "github", "star", "acme/api", "background", nil),
		ev("5", "jira", "issue", "", "standard", nil),
	}
	s, err := h.Summarize(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "5 notifications held back" || len(s.Groups) != 3 {
		t.Fatalf("summary = %+v", s)
	}
	top := s.Groups[0]
	if top.Key != "github/acme/web" || top.Count != 3 || len(top.EventIDs) != 3 || !strings.Contains(top.Summary, "2 pull_request, 1 push") {
		t.Fatalf("top group = %+v", top)
	}
	if !strings.Contains(s.Text, "4 github, 1 jira") || !strings.Contains(s.Text, "Busiest: acme/web (github) with 3") {
		t.Fatalf("text = %q", s.Text)
	}
	if empty, _ := h.Summarize(ctx, nil); empty.Text != "Nothing was held back." {
		t.Fatalf("empty = %+v", empty)
	}
	one, _ := h.Summarize(ctx, events[:1])
	if one.Title != "1 notification held back" {
		t.Fatalf("singular = %q", one.Title)
	}
}

func TestExplanationsAreSpecificAndHonestAboutAI(t *testing.T) {
	h := NewHeuristic()
	state := func(status, reason, rule, model string, conf float64) *event.DecisionState {
		return &event.DecisionState{Status: status, Reason: reason, MatchedRule: rule, ModelVersion: model, Confidence: conf}
	}
	cases := []struct {
		name string
		e    event.Event
		want []string
		not  []string
	}{
		{"critical", event.Event{AttentionClass: "critical", DecisionState: state("sent", "security vulnerability detected", "github-security-advisory", "", 0)},
			[]string{"critical", "every channel at once", `"github-security-advisory"`}, []string{"Attention Intelligence"}},
		{"merged", event.Event{AttentionClass: "standard", MergedInto: "evt-1", DecisionState: state("merged", "duplicate", "", "", 0)},
			[]string{"merged", "evt-1"}, nil},
		{"digest by the heuristic", event.Event{AttentionClass: "background", DecisionState: state("digest", "default routing; attention: scored 15", "default-github", ModelHeuristic, 0.8)},
			[]string{"digest", "heuristic-v1", "80%"}, []string{"was not used"}},
		{"delayed", event.Event{AttentionClass: "standard", DecisionState: state("delayed", "quiet hours", "", "", 0)},
			[]string{"quiet hours end", "was not used"}, nil},
		{"queued by a rule", event.Event{AttentionClass: "standard", DecisionState: state("queued", "default routing", "default-github", "", 0)},
			[]string{"queued", "was not used"}, nil},
		{"no rule", event.Event{AttentionClass: "standard", DecisionState: state("queued", "", "", "", 0)},
			[]string{"No rule matched"}, nil},
	}
	for _, c := range cases {
		got, err := h.Explain(ctx, c.e)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q must not contain %q", c.name, got, n)
			}
		}
	}
	if got, _ := h.Explain(ctx, event.Event{}); !strings.Contains(got, "No decision") {
		t.Fatalf("no decision = %q", got)
	}
}

func TestCountsTruncates(t *testing.T) {
	got := counts(map[string]int{"a": 5, "b": 4, "c": 3, "d": 2, "": 1}, 3)
	if got != "5 a, 4 b, 3 c, and 2 more" {
		t.Fatalf("counts = %q", got)
	}
}

// ---- digests ----

type listEvents []event.Event

func (l listEvents) List(context.Context) ([]event.Event, error) { return l, nil }

type recordingNotifier struct {
	titles []string
	bodies []string
	err    error
}

func (r *recordingNotifier) Notify(_ context.Context, id, title, body string) error {
	r.titles, r.bodies = append(r.titles, title), append(r.bodies, body)
	return r.err
}

var t0 = time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)

func held(id, typ string, at time.Time, status string, payload map[string]any) event.Event {
	e := ev(id, "github", typ, "acme/web", "background", payload)
	e.CreatedAt = at
	e.DecisionState = &event.DecisionState{Status: status}
	return e
}

func newGen(events []event.Event, n Notifier) (*Generator, *MemoryDigestStore) {
	store := NewMemoryDigestStore()
	h := NewHeuristic()
	g := NewGenerator(listEvents(events), store, h, h, n, nil).WithClock(func() time.Time { return t0.Add(10 * time.Hour) })
	return g, store
}

func TestDigestCoversOnlyHeldEventsAndSkipsCriticalAndMerged(t *testing.T) {
	crit := held("crit", "security_advisory", t0.Add(time.Hour), event.DecisionDigest, nil)
	crit.AttentionClass = event.AttentionCritical
	merged := held("dup", "push", t0.Add(time.Hour), event.DecisionDigest, nil)
	merged.MergedInto = "a"
	noState := ev("ns", "github", "push", "", "standard", nil)
	noState.CreatedAt = t0.Add(time.Hour)
	queued := held("queued", "push", t0.Add(time.Hour), event.DecisionQueued, nil)
	future := held("future", "push", t0.Add(20*time.Hour), event.DecisionDigest, nil)

	g, store := newGen([]event.Event{
		held("a", "star", t0.Add(time.Hour), event.DecisionDigest, nil),
		held("b", "push", t0.Add(2*time.Hour), event.DecisionDelayed, nil),
		crit, merged, noState, queued, future,
	}, nil)
	d, made, err := g.Generate(ctx, "ann")
	if err != nil || !made {
		t.Fatalf("generate = %v %v", made, err)
	}
	if d.Count != 2 || d.CreatedBy != "ann" || d.Model != ModelHeuristic || d.ID == "" {
		t.Fatalf("digest = %+v", d)
	}
	var ids []string
	for _, grp := range d.Groups {
		ids = append(ids, grp.EventIDs...)
	}
	if strings.Join(ids, ",") != "a,b" {
		t.Fatalf("covered events = %v; a critical, merged, queued or future event must not be in a digest", ids)
	}
	if list, _ := store.List(ctx, 10); len(list) != 1 {
		t.Fatal("the digest must be stored")
	}
}

func TestEachDigestStartsWhereTheLastEnded(t *testing.T) {
	events := []event.Event{held("a", "push", t0.Add(time.Hour), event.DecisionDigest, nil)}
	g, _ := newGen(events, nil)
	first, made, _ := g.Generate(ctx, "x")
	if !made || first.Count != 1 {
		t.Fatalf("first = %+v", first)
	}
	if _, made, _ := g.Generate(ctx, "x"); made {
		t.Fatal("nothing new: no second digest")
	}
	g.events = listEvents(append(events, held("b", "push", t0.Add(11*time.Hour), event.DecisionDigest, nil)))
	g.now = func() time.Time { return t0.Add(12 * time.Hour) }
	second, made, _ := g.Generate(ctx, "x")
	if !made || second.Count != 1 || !second.From.Equal(first.To) {
		t.Fatalf("second = %+v (first ended %v)", second, first.To)
	}
}

func TestNothingHeldMeansNoDigest(t *testing.T) {
	g, store := newGen(nil, nil)
	if _, made, err := g.Generate(ctx, "x"); made || err != nil {
		t.Fatalf("made %v err %v", made, err)
	}
	if list, _ := store.List(ctx, 10); len(list) != 0 {
		t.Fatal("an empty digest must not be stored")
	}
}

func TestDigestIsHandedToDeliveryAndFailureIsTolerated(t *testing.T) {
	n := &recordingNotifier{}
	g, _ := newGen([]event.Event{held("a", "star", t0.Add(time.Hour), event.DecisionDigest, nil)}, n)
	d, _, _ := g.Generate(ctx, "x")
	if len(n.titles) != 1 || !strings.HasPrefix(n.titles[0], "Attention digest: ") || !strings.Contains(n.bodies[0], "held back") {
		t.Fatalf("notified = %v %v", n.titles, n.bodies)
	}
	_ = d

	bad := &recordingNotifier{err: errors.New("queue down")}
	g, store := newGen([]event.Event{held("a", "star", t0.Add(time.Hour), event.DecisionDigest, nil)}, bad)
	if _, made, err := g.Generate(ctx, "x"); err != nil || !made {
		t.Fatalf("a delivery failure must not lose the digest: %v %v", made, err)
	}
	if list, _ := store.List(ctx, 1); len(list) != 1 {
		t.Fatal("the digest must still be stored")
	}
}

func TestHighlightsLeadWithWhatMattersAndNeverIncludeNoise(t *testing.T) {
	g, _ := newGen([]event.Event{
		held("noise", "star", t0.Add(time.Hour), event.DecisionDigest, nil),
		held("review", "pull_request", t0.Add(2*time.Hour), event.DecisionDigest, map[string]any{"action": "review_requested"}),
		held("failing", "check_run", t0.Add(3*time.Hour), event.DecisionDigest, map[string]any{"check_run": map[string]any{"conclusion": "failure"}}),
	}, nil)
	d, _, _ := g.Generate(ctx, "x")
	if len(d.Highlights) != 2 || d.Highlights[0].EventID != "failing" || d.Highlights[1].EventID != "review" {
		t.Fatalf("highlights = %+v", d.Highlights)
	}
	if !strings.Contains(Message(d), "Look at first:") {
		t.Fatal("the delivered text must lead with the highlights")
	}
}

func TestDigestIsBounded(t *testing.T) {
	var events []event.Event
	for i := 0; i < maxDigestEvents+20; i++ {
		events = append(events, held(fmt.Sprintf("e%03d", i), "push", t0.Add(time.Duration(i+1)*time.Second), event.DecisionDigest, nil))
	}
	g, _ := newGen(events, nil)
	first, _, _ := g.Generate(ctx, "x")
	if first.Count != maxDigestEvents {
		t.Fatalf("count = %d", first.Count)
	}
	second, made, _ := g.Generate(ctx, "x")
	if !made || second.Count != 20 {
		t.Fatalf("the rest must go in the next digest: %+v", second)
	}
}

func TestMessageIsBounded(t *testing.T) {
	d := Digest{Summary: strings.Repeat("x", 5000)}
	if got := Message(d); len([]rune(got)) > maxMessageChars {
		t.Fatalf("message has %d characters", len([]rune(got)))
	}
}

func TestSchedulerWithAZeroIntervalNeverRuns(t *testing.T) {
	g, store := newGen([]event.Event{held("a", "star", t0.Add(time.Hour), event.DecisionDigest, nil)}, nil)
	done := make(chan struct{})
	go func() {
		NewScheduler(g, 0, nil).Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a zero interval must return at once")
	}
	if list, _ := store.List(ctx, 1); len(list) != 0 {
		t.Fatal("nothing may be generated")
	}
}

func TestSchedulerGeneratesOnItsInterval(t *testing.T) {
	g, store := newGen([]event.Event{held("a", "star", t0.Add(time.Hour), event.DecisionDigest, nil)}, nil)
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go NewScheduler(g, 20*time.Millisecond, nil).Run(c)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if list, _ := store.List(ctx, 1); len(list) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the scheduler did not generate a digest")
}

func TestDigestStoreGet(t *testing.T) {
	s := NewMemoryDigestStore()
	s.Add(ctx, Digest{ID: "1"})
	s.Add(ctx, Digest{ID: "2"})
	if list, _ := s.List(ctx, 0); len(list) != 2 || list[0].ID != "2" {
		t.Fatalf("list = %+v", list)
	}
	if d, err := s.Get(ctx, "1"); err != nil || d.ID != "1" {
		t.Fatalf("get = %v", err)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrDigestNotFound) {
		t.Fatalf("get missing = %v", err)
	}
}
