// Package insights turns the notification history into numbers and a timeline:
// what arrived, what was held back, what interrupted a person, and what people
// did about it. It only reads; the pipeline remains the single author of decisions.
package insights

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/example/notification-service/pkg/inbox"
)

// ErrInvalidRange is returned for a range other than 24h, 7d or 30d.
var ErrInvalidRange = errors.New("range must be 24h, 7d or 30d")

// Range names how far back analytics look.
type Range string

const (
	Range24h Range = "24h"
	Range7d  Range = "7d"
	Range30d Range = "30d"
)

const (
	topN                 = 5
	defaultActivityLimit = 50
	maxActivityLimit     = 200
)

type rangeSpec struct {
	span    time.Duration
	buckets int
}

var specs = map[Range]rangeSpec{
	Range24h: {span: 24 * time.Hour, buckets: 24},
	Range7d:  {span: 7 * 24 * time.Hour, buckets: 7},
	Range30d: {span: 30 * 24 * time.Hour, buckets: 30},
}

// ParseRange validates a range. An empty value means 7d.
func ParseRange(s string) (Range, error) {
	if s == "" {
		return Range7d, nil
	}
	r := Range(s)
	if _, ok := specs[r]; !ok {
		return "", ErrInvalidRange
	}
	return r, nil
}

// Source is what insights read. *inbox.Service satisfies it.
type Source interface {
	Items(ctx context.Context) ([]inbox.Item, error)
	RecentActions(ctx context.Context, limit int) ([]inbox.TrailRow, error)
}

// Service computes analytics and the activity feed.
type Service struct {
	src Source
	now func() time.Time
}

func NewService(src Source) *Service {
	return &Service{src: src, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock replaces the clock; for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Totals count notifications in the range by level and by what the pipeline did.
type Totals struct {
	Events     int `json:"events"`
	Critical   int `json:"critical"`
	High       int `json:"high"`
	Normal     int `json:"normal"`
	Low        int `json:"low"`
	Delivered  int `json:"delivered"`
	Held       int `json:"held"`
	Suppressed int `json:"suppressed"`
	Merged     int `json:"merged"`
	Recorded   int `json:"recorded"`
	Unread     int `json:"unread"`
}

// Bucket is one step of the time series.
type Bucket struct {
	Start    time.Time `json:"start"`
	Critical int       `json:"critical"`
	High     int       `json:"high"`
	Normal   int       `json:"normal"`
	Low      int       `json:"low"`
}

// Ranked is a name with a count, for top lists.
type Ranked struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Attention says how much interruption the platform avoided and how people responded.
type Attention struct {
	// Interruptions is how many notifications were delivered to a person.
	Interruptions int `json:"interruptions"`
	// HeldBack is how many did not interrupt anyone (held, merged, suppressed or recorded).
	HeldBack int `json:"heldBack"`
	// HeldBackPercent is HeldBack as a share of all events, 0 when there are none.
	HeldBackPercent float64 `json:"heldBackPercent"`
	ReadCount       int     `json:"readCount"`
	// MedianReadSeconds is the median time from arrival to being read; null when nothing was read.
	MedianReadSeconds *int64 `json:"medianReadSeconds"`
	Acknowledged      int    `json:"acknowledged"`
	Resolved          int    `json:"resolved"`
	// OpenCritical is critical notifications not yet acknowledged or resolved.
	OpenCritical int `json:"openCritical"`
}

// Analytics is the full answer for one range.
type Analytics struct {
	Range           Range     `json:"range"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	BucketSeconds   int64     `json:"bucketSeconds"`
	Totals          Totals    `json:"totals"`
	Attention       Attention `json:"attention"`
	Series          []Bucket  `json:"series"`
	TopSources      []Ranked  `json:"topSources"`
	TopRepositories []Ranked  `json:"topRepositories"`
	TopEvents       []Ranked  `json:"topEvents"`
}

// Analytics computes the numbers for the range ending now.
func (s *Service) Analytics(ctx context.Context, r Range) (Analytics, error) {
	if _, ok := specs[r]; !ok {
		return Analytics{}, ErrInvalidRange
	}
	items, err := s.src.Items(ctx)
	if err != nil {
		return Analytics{}, err
	}
	return Analyze(items, s.now(), r), nil
}

// Analyze is the pure computation behind Analytics.
func Analyze(items []inbox.Item, now time.Time, r Range) Analytics {
	spec := specs[r]
	from := now.Add(-spec.span)
	width := spec.span / time.Duration(spec.buckets)
	out := Analytics{
		Range: r, From: from, To: now, BucketSeconds: int64(width / time.Second),
		Series: make([]Bucket, spec.buckets),
	}
	for i := range out.Series {
		out.Series[i].Start = from.Add(time.Duration(i) * width)
	}

	sources, repos, events := map[string]int{}, map[string]int{}, map[string]int{}
	var readSeconds []int64
	for _, it := range items {
		if it.CreatedAt.Before(from) || it.CreatedAt.After(now) {
			continue
		}
		out.Totals.Events++
		countLevel(&out.Totals, it.Priority)
		countOutcome(&out.Totals, it.Outcome)
		if !it.Read {
			out.Totals.Unread++
		}
		idx := int(it.CreatedAt.Sub(from) / width)
		if idx >= spec.buckets {
			idx = spec.buckets - 1
		}
		bucketAdd(&out.Series[idx], it.Priority)

		sources[it.Source]++
		if it.Repository != "" {
			repos[it.Repository]++
		}
		events[it.EventType]++

		if it.ReadAt != nil && !it.ReadAt.Before(it.CreatedAt) {
			readSeconds = append(readSeconds, int64(it.ReadAt.Sub(it.CreatedAt)/time.Second))
		}
		if it.AcknowledgedAt != nil {
			out.Attention.Acknowledged++
		}
		if it.ResolvedAt != nil {
			out.Attention.Resolved++
		}
		if it.Priority == inbox.LevelCritical && it.AcknowledgedAt == nil && it.ResolvedAt == nil {
			out.Attention.OpenCritical++
		}
	}

	out.Attention.Interruptions = out.Totals.Delivered
	out.Attention.HeldBack = out.Totals.Events - out.Totals.Delivered
	if out.Totals.Events > 0 {
		out.Attention.HeldBackPercent = float64(out.Attention.HeldBack) * 100 / float64(out.Totals.Events)
	}
	out.Attention.ReadCount = len(readSeconds)
	if len(readSeconds) > 0 {
		sort.Slice(readSeconds, func(i, j int) bool { return readSeconds[i] < readSeconds[j] })
		median := readSeconds[len(readSeconds)/2]
		out.Attention.MedianReadSeconds = &median
	}
	out.TopSources, out.TopRepositories, out.TopEvents = top(sources), top(repos), top(events)
	return out
}

func countLevel(t *Totals, l inbox.Level) {
	switch l {
	case inbox.LevelCritical:
		t.Critical++
	case inbox.LevelHigh:
		t.High++
	case inbox.LevelNormal:
		t.Normal++
	default:
		t.Low++
	}
}

func countOutcome(t *Totals, o inbox.Outcome) {
	switch o {
	case inbox.OutcomeDelivered:
		t.Delivered++
	case inbox.OutcomeHeld:
		t.Held++
	case inbox.OutcomeSuppressed:
		t.Suppressed++
	case inbox.OutcomeMerged:
		t.Merged++
	default:
		t.Recorded++
	}
}

func bucketAdd(b *Bucket, l inbox.Level) {
	switch l {
	case inbox.LevelCritical:
		b.Critical++
	case inbox.LevelHigh:
		b.High++
	case inbox.LevelNormal:
		b.Normal++
	default:
		b.Low++
	}
}

// top returns the topN names by count, ties broken by name so the order is stable.
func top(counts map[string]int) []Ranked {
	out := make([]Ranked, 0, len(counts))
	for name, n := range counts {
		out = append(out, Ranked{Name: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

// Entry kinds in the activity feed.
const (
	KindReceived = "received"
	KindAction   = "action"
)

// Entry is one line of the activity timeline.
type Entry struct {
	At         time.Time     `json:"at"`
	Kind       string        `json:"kind"`
	Step       string        `json:"step,omitempty"`
	Detail     string        `json:"detail,omitempty"`
	EventID    string        `json:"eventId"`
	Source     string        `json:"source"`
	EventType  string        `json:"eventType"`
	Repository string        `json:"repository,omitempty"`
	Priority   inbox.Level   `json:"priority"`
	Outcome    inbox.Outcome `json:"outcome,omitempty"`
	Reason     string        `json:"reason,omitempty"`
}

// ParseLimit validates the feed size. Zero means the default.
func ParseLimit(n int) (int, error) {
	if n == 0 {
		return defaultActivityLimit, nil
	}
	if n < 0 || n > maxActivityLimit {
		return 0, errors.New("limit must be between 1 and 200")
	}
	return n, nil
}

// Activity merges arrivals and people's actions into one newest-first timeline.
func (s *Service) Activity(ctx context.Context, limit int) ([]Entry, error) {
	items, err := s.src.Items(ctx)
	if err != nil {
		return nil, err
	}
	actions, err := s.src.RecentActions(ctx, limit)
	if err != nil {
		return nil, err
	}
	return BuildActivity(items, actions, limit), nil
}

// BuildActivity is the pure merge behind Activity. Actions on notifications that
// are no longer visible are dropped, since there is nothing left to describe.
func BuildActivity(items []inbox.Item, actions []inbox.TrailRow, limit int) []Entry {
	byID := make(map[string]inbox.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	entries := make([]Entry, 0, len(items)+len(actions))
	for _, it := range items {
		e := base(it)
		e.At, e.Kind, e.Outcome = it.CreatedAt, KindReceived, it.Outcome
		if it.DecisionState != nil {
			e.Reason = it.DecisionState.Reason
		}
		entries = append(entries, e)
	}
	for _, a := range actions {
		it, ok := byID[a.EventID]
		if !ok {
			continue
		}
		e := base(it)
		e.At, e.Kind, e.Step, e.Detail = a.At, KindAction, a.Step, a.Detail
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.After(entries[j].At) })
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func base(it inbox.Item) Entry {
	return Entry{EventID: it.ID, Source: it.Source, EventType: it.EventType, Repository: it.Repository, Priority: it.Priority}
}
