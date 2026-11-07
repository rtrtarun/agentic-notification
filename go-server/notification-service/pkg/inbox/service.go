package inbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/pagination"
)

const (
	maxSnooze    = 30 * 24 * time.Hour
	minSnooze    = time.Minute
	maxRelated   = 10
	maxBulkItems = 100
)

// Service answers inbox queries and applies people's actions. It reads events
// through event.Repository and writes only its own state.
type Service struct {
	events event.Repository
	state  Repository
	now    func() time.Time
}

func NewService(events event.Repository, state Repository) *Service {
	return &Service{events: events, state: state, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock replaces the clock; for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// MaxBulk is the most ids one bulk request may carry.
func MaxBulk() int { return maxBulkItems }

// view is an event joined with its state.
type view struct {
	e event.Event
	s State
}

func (s *Service) load(ctx context.Context) ([]view, map[string]int, error) {
	events, err := s.events.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	states, err := s.state.All(ctx)
	if err != nil {
		return nil, nil, err
	}
	repeats := map[string]int{}
	for _, e := range events {
		if e.MergedInto != "" {
			repeats[e.MergedInto]++
		}
	}
	out := make([]view, 0, len(events))
	for _, e := range events {
		st, ok := states[e.ID]
		if !ok {
			st = State{EventID: e.ID}
		}
		if st.DeletedAt != nil {
			continue
		}
		out = append(out, view{e: e, s: st})
	}
	return out, repeats, nil
}

func (s *Service) item(v view, repeats map[string]int) Item {
	updated := v.e.CreatedAt
	if v.s.UpdatedAt.After(updated) {
		updated = v.s.UpdatedAt
	}
	return Item{
		ID: v.e.ID, Source: v.e.Source, EventType: v.e.EventType, Repository: v.e.Repository,
		AttentionClass: v.e.AttentionClass, Priority: LevelOf(v.e, v.s), Outcome: OutcomeOf(v.e),
		DecisionState: v.e.DecisionState, ThreadID: v.e.ThreadID, MergedInto: v.e.MergedInto,
		RepeatCount: repeats[v.e.ID], CreatedAt: v.e.CreatedAt, UpdatedAt: updated,
		Read: v.s.ReadAt != nil, ReadAt: v.s.ReadAt, ArchivedAt: v.s.ArchivedAt,
		AcknowledgedAt: v.s.AcknowledgedAt, ResolvedAt: v.s.ResolvedAt, SnoozedUntil: v.s.SnoozedUntil,
		PriorityOverride: v.s.PriorityOverride,
	}
}

// List returns one page of items. Repeats merged into an earlier event are left
// out unless the query asks for the merged outcome; each original carries a repeat count.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	views, repeats, err := s.load(ctx)
	if err != nil {
		return Page{}, err
	}
	now := s.now()
	page := Page{Counts: s.counts(views, now)}

	wantMerged := false
	for _, o := range q.Outcomes {
		if o == OutcomeMerged {
			wantMerged = true
		}
	}

	var matched []Item
	for _, v := range views {
		if (v.s.ArchivedAt != nil) != q.Archived {
			continue
		}
		if v.e.MergedInto != "" && !wantMerged {
			continue
		}
		it := s.item(v, repeats)
		if matches(it, q) {
			matched = append(matched, it)
		}
	}
	sortItems(matched, q.Sort)
	newestFirst := q.Sort == "" || q.Sort == SortNewest
	var after *pagination.Cursor
	if q.Cursor != "" {
		if !newestFirst {
			return Page{}, fmt.Errorf("%w: cursor paging needs the newest-first sort", ErrInvalid)
		}
		c, err := pagination.Decode(q.Cursor)
		if err != nil {
			return Page{}, fmt.Errorf("%w: the cursor is not valid", ErrInvalid)
		}
		after = &c
		// A cursor names one position, so equal times are ordered by ID.
		sort.SliceStable(matched, func(i, j int) bool {
			return pagination.Newer(matched[i].CreatedAt, matched[i].ID, matched[j].CreatedAt, matched[j].ID)
		})
		kept := matched[:0:0]
		for _, it := range matched {
			if after.Before(it.CreatedAt, it.ID) {
				kept = append(kept, it)
			}
		}
		matched = kept
	}

	size := q.PageSize
	if size < 1 {
		size = DefaultPageSize
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	pages := (len(matched) + size - 1) / size
	n := q.Page
	if n < 1 {
		n = 1
	}
	if pages > 0 && n > pages {
		n = pages
	}
	from, to := (n-1)*size, n*size
	if from > len(matched) {
		from = len(matched)
	}
	if to > len(matched) {
		to = len(matched)
	}
	if after != nil {
		n, from, to = 1, 0, min(size, len(matched))
	}
	if newestFirst && to < len(matched) && to > 0 {
		last := matched[to-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{At: last.CreatedAt, ID: last.ID})
	}
	page.Items = matched[from:to]
	if page.Items == nil {
		page.Items = []Item{}
	}
	page.Total, page.Page, page.PageSize = len(matched), n, size
	return page, nil
}

func matches(it Item, q Query) bool {
	if len(q.Priorities) > 0 && !containsLevel(q.Priorities, it.Priority) {
		return false
	}
	if len(q.Outcomes) > 0 && !containsOutcome(q.Outcomes, it.Outcome) {
		return false
	}
	if q.Read != nil && it.Read != *q.Read {
		return false
	}
	if q.Source != "" && !strings.EqualFold(it.Source, q.Source) {
		return false
	}
	if q.Repository != "" && !strings.EqualFold(it.Repository, q.Repository) {
		return false
	}
	if q.From != nil && it.CreatedAt.Before(*q.From) {
		return false
	}
	if q.To != nil && it.CreatedAt.After(*q.To) {
		return false
	}
	terms := strings.Fields(strings.ToLower(q.Text))
	if len(terms) == 0 {
		return true
	}
	text := searchText(it)
	for _, t := range terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}

func searchText(it Item) string {
	parts := []string{it.ID, it.Source, strings.ReplaceAll(it.EventType, "_", " "), it.EventType, it.Repository,
		string(it.Priority), string(it.Outcome)}
	if it.DecisionState != nil {
		parts = append(parts, it.DecisionState.Status, it.DecisionState.Reason, it.DecisionState.MatchedRule)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func containsLevel(ls []Level, l Level) bool {
	for _, x := range ls {
		if x == l {
			return true
		}
	}
	return false
}

func containsOutcome(os []Outcome, o Outcome) bool {
	for _, x := range os {
		if x == o {
			return true
		}
	}
	return false
}

func sortItems(items []Item, order string) {
	newest := func(i, j int) bool {
		return pagination.Newer(items[i].CreatedAt, items[i].ID, items[j].CreatedAt, items[j].ID)
	}
	sort.SliceStable(items, func(i, j int) bool {
		switch order {
		case SortOldest:
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		case SortPriority:
			if ri, rj := levelRank(items[i].Priority), levelRank(items[j].Priority); ri != rj {
				return ri < rj
			}
		case SortLowest:
			if ri, rj := levelRank(items[i].Priority), levelRank(items[j].Priority); ri != rj {
				return ri > rj
			}
		case SortUpdated:
			if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
				return items[i].UpdatedAt.After(items[j].UpdatedAt)
			}
		}
		return newest(i, j)
	})
}

func (s *Service) counts(views []view, now time.Time) Counts {
	var c Counts
	for _, v := range views {
		if v.s.ArchivedAt != nil {
			c.Archived++
			continue
		}
		if v.e.MergedInto != "" {
			continue
		}
		c.All++
		snoozed := v.s.SnoozedUntil != nil && v.s.SnoozedUntil.After(now)
		if v.s.ReadAt == nil && !snoozed {
			c.Unread++
		}
		switch LevelOf(v.e, v.s) {
		case LevelCritical:
			c.Critical++
		case LevelHigh:
			c.High++
		case LevelNormal:
			c.Normal++
		default:
			c.Low++
		}
		switch OutcomeOf(v.e) {
		case OutcomeHeld:
			c.Held++
		case OutcomeDelivered:
			c.Delivered++
		case OutcomeSuppressed:
			c.Suppressed++
		}
	}
	return c
}

// Get returns one notification with its trail and related events.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	views, repeats, err := s.load(ctx)
	if err != nil {
		return Detail{}, err
	}
	var self *view
	for i := range views {
		if views[i].e.ID == id {
			self = &views[i]
			break
		}
	}
	if self == nil {
		return Detail{}, ErrNotFound
	}
	recorded, err := s.state.Trail(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	detail := Detail{Item: s.item(*self, repeats), Trail: append(reconstruct(self.e), recorded...), Related: []Item{}}

	// Related: same thread, same dedup key, or the same repository, nearest first.
	var related []Item
	for _, v := range views {
		if v.e.ID == id {
			continue
		}
		sameThread := self.e.ThreadID != "" && v.e.ThreadID == self.e.ThreadID
		sameKey := self.e.DedupKey != "" && v.e.DedupKey == self.e.DedupKey
		sameRepo := self.e.Repository != "" && v.e.Repository == self.e.Repository
		if sameThread || sameKey || sameRepo {
			related = append(related, s.item(v, repeats))
		}
	}
	sortItems(related, SortNewest)
	if len(related) > maxRelated {
		related = related[:maxRelated]
	}
	if related != nil {
		detail.Related = related
	}
	return detail, nil
}

// reconstruct derives the processing steps from what is stored about the event.
// The store keeps one timestamp, so every step carries it and is marked reconstructed.
func reconstruct(e event.Event) []TrailEntry {
	at := e.CreatedAt
	steps := []TrailEntry{
		{Step: "received", Detail: e.Source + " " + e.EventType, At: at, Reconstructed: true},
		{Step: "classified", Detail: e.AttentionClass, At: at, Reconstructed: true},
	}
	rule := "no rule matched"
	if e.DecisionState != nil && e.DecisionState.MatchedRule != "" {
		rule = e.DecisionState.MatchedRule
	}
	steps = append(steps, TrailEntry{Step: "rules", Detail: rule, At: at, Reconstructed: true})
	dedup := "not a repeat"
	if e.MergedInto != "" {
		dedup = "merged into " + e.MergedInto
	}
	steps = append(steps, TrailEntry{Step: "dedup", Detail: dedup, At: at, Reconstructed: true})
	if e.DecisionState != nil {
		steps = append(steps, TrailEntry{Step: "outcome", Detail: e.DecisionState.Status, At: at, Reconstructed: true})
	}
	return steps
}

// Apply performs one action. Every action except read and unread is added to the trail.
func (s *Service) Apply(ctx context.Context, id string, a Action) error {
	views, _, err := s.load(ctx)
	if err != nil {
		return err
	}
	for _, v := range views {
		if v.e.ID == id {
			return s.apply(ctx, v, a)
		}
	}
	return ErrNotFound
}

func (s *Service) apply(ctx context.Context, v view, a Action) error {
	now := s.now()
	st := v.s
	detail := ""
	switch a.Type {
	case ActionRead:
		if st.ReadAt == nil {
			st.ReadAt = &now
		}
	case ActionUnread:
		st.ReadAt = nil
	case ActionArchive:
		st.ArchivedAt = &now
	case ActionUnarchive:
		st.ArchivedAt = nil
	case ActionDelete:
		st.DeletedAt = &now
	case ActionAcknowledge:
		st.AcknowledgedAt = &now
		st.ReadAt = firstTime(st.ReadAt, now)
	case ActionResolve:
		st.ResolvedAt = &now
		st.ReadAt = firstTime(st.ReadAt, now)
	case ActionSnooze:
		d := a.Until.Sub(now)
		if d < minSnooze || d > maxSnooze {
			return fmt.Errorf("%w: snooze must be between 1 minute and 30 days", ErrInvalid)
		}
		until := a.Until.UTC()
		st.SnoozedUntil = &until
		detail = "until " + until.Format(time.RFC3339)
	case ActionUnsnooze:
		st.SnoozedUntil = nil
	case ActionPriority:
		if v.e.AttentionClass == event.AttentionCritical {
			return fmt.Errorf("%w: critical events cannot be reprioritised", ErrInvalid)
		}
		if a.Priority != "" && (!ValidLevel(a.Priority) || a.Priority == LevelCritical) {
			return fmt.Errorf("%w: priority must be high, normal or low", ErrInvalid)
		}
		st.PriorityOverride = a.Priority
		detail = string(a.Priority)
		if detail == "" {
			detail = "cleared"
		}
	default:
		return fmt.Errorf("%w: unknown action %q", ErrInvalid, a.Type)
	}
	st.UpdatedAt = now
	if err := s.state.Save(ctx, st); err != nil {
		return err
	}
	if a.Type == ActionRead || a.Type == ActionUnread {
		return nil
	}
	return s.state.AppendTrail(ctx, v.e.ID, TrailEntry{Step: string(a.Type), Detail: detail, At: now})
}

func firstTime(existing *time.Time, now time.Time) *time.Time {
	if existing != nil {
		return existing
	}
	return &now
}

// publicError keeps storage error text out of responses.
func publicError(err error) string {
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		return err.Error()
	}
	return "could not apply the action"
}

// MarkAllRead marks every visible, unarchived event read and returns how many changed.
func (s *Service) MarkAllRead(ctx context.Context) (int, error) {
	views, _, err := s.load(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, v := range views {
		if v.s.ReadAt != nil || v.s.ArchivedAt != nil {
			continue
		}
		if err := s.apply(ctx, v, Action{Type: ActionRead}); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// Bulk applies one action to many ids. A failure on one id does not stop the others.
func (s *Service) Bulk(ctx context.Context, ids []string, a Action) ([]BulkResult, error) {
	if len(ids) == 0 || len(ids) > maxBulkItems {
		return nil, fmt.Errorf("%w: ids must hold 1 to %d entries", ErrInvalid, maxBulkItems)
	}
	views, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]view, len(views))
	for _, v := range views {
		byID[v.e.ID] = v
	}
	results := make([]BulkResult, 0, len(ids))
	for _, id := range ids {
		v, ok := byID[id]
		if !ok {
			results = append(results, BulkResult{ID: id, Error: ErrNotFound.Error()})
			continue
		}
		if err := s.apply(ctx, v, a); err != nil {
			results = append(results, BulkResult{ID: id, Error: publicError(err)})
			continue
		}
		results = append(results, BulkResult{ID: id, OK: true})
	}
	return results, nil
}

// Items returns every visible notification, archived ones included, newest
// first, for analytics. Hidden (deleted) notifications are left out.
func (s *Service) Items(ctx context.Context) ([]Item, error) {
	views, repeats, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(views))
	for _, v := range views {
		items = append(items, s.item(v, repeats))
	}
	sortItems(items, SortNewest)
	return items, nil
}

// RecentActions returns the newest user actions across all notifications.
func (s *Service) RecentActions(ctx context.Context, limit int) ([]TrailRow, error) {
	return s.state.RecentTrail(ctx, limit)
}
