package feedback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/example/notification-service/pkg/event"
	"github.com/example/notification-service/pkg/overrides"
)

// Overrides is the part of pkg/overrides the service needs.
type Overrides interface {
	Upsert(ctx context.Context, o overrides.Override) (overrides.Override, error)
	Delete(ctx context.Context, id string) error
}

// Lookup finds an event by id. It returns ErrEventNotFound when there is none.
type Lookup func(ctx context.Context, id string) (event.Event, error)

// Service records feedback and applies its consequences.
type Service struct {
	store  Store
	lookup Lookup
	ov     Overrides
	now    func() time.Time
}

// NewService builds a service. ov may be nil, in which case feedback is only recorded.
func NewService(store Store, lookup Lookup, ov Overrides) *Service {
	return &Service{store: store, lookup: lookup, ov: ov, now: time.Now}
}

// WithClock replaces the clock; for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Input is one feedback request.
type Input struct {
	EventID string
	Action  string
	// Scope is how widely always/never applies: type (default), repository or source.
	Scope  string
	Reason string
}

// Result reports what recording did.
type Result struct {
	Feedback EventFeedback
	// Override is the override created or replaced by always/never.
	Override *overrides.Override
	// OverrideSkipped is true when always/never was recorded as a signal only,
	// because the caller may not create overrides.
	OverrideSkipped bool
	// Reverted is true when undo removed an override.
	Reverted bool
}

// Record stores a signal. actor names the person (or the API key). allowOverride
// says whether this caller may create or remove overrides; when false the signal
// is still stored, so the learning layer sees it.
func (s *Service) Record(ctx context.Context, actor string, in Input, allowOverride bool) (Result, error) {
	if err := validateInput(in); err != nil {
		return Result{}, err
	}
	evt, err := s.lookup(ctx, in.EventID)
	if err != nil {
		return Result{}, err
	}
	fb := EventFeedback{
		ID: newID(), EventID: evt.ID, UserID: actor, Action: in.Action, Scope: in.Scope, Reason: strings.TrimSpace(in.Reason),
		Source: evt.Source, EventType: evt.EventType, Repository: evt.Repository, CreatedAt: s.now().UTC(),
	}
	res := Result{}

	switch in.Action {
	case ActionAlways, ActionNever:
		if in.Action == ActionNever && evt.AttentionClass == event.AttentionCritical {
			return Result{}, fmt.Errorf("%w: critical events cannot be muted", ErrInvalid)
		}
		if fb.Scope == "" {
			fb.Scope = ScopeType
		}
		switch {
		case s.ov == nil || !allowOverride:
			res.OverrideSkipped = true
		default:
			o, err := s.ov.Upsert(ctx, overrideFor(evt, in.Action, fb.Scope, actor))
			if err != nil {
				return Result{}, err
			}
			fb.OverrideID, res.Override = o.ID, &o
		}
	case ActionUndo:
		reverted, err := s.undo(ctx, actor, evt.ID, allowOverride)
		if err != nil {
			return Result{}, err
		}
		res.Reverted = reverted
	}

	if err := s.store.Add(ctx, fb); err != nil {
		return Result{}, err
	}
	res.Feedback = fb
	return res, nil
}

// RecordOpened notes that a person opened an event, once per person and event.
func (s *Service) RecordOpened(ctx context.Context, actor, eventID string) error {
	recent, err := s.store.List(ctx, Filter{EventID: eventID, UserID: actor, Limit: 50})
	if err != nil {
		return err
	}
	for _, f := range recent {
		if f.Action == ActionOpened {
			return nil
		}
	}
	_, err = s.Record(ctx, actor, Input{EventID: eventID, Action: ActionOpened}, false)
	return err
}

// undo reverts this person's latest always/never on the event. Undoing something
// already undone, or undoing a dismissal, only records the signal.
func (s *Service) undo(ctx context.Context, actor, eventID string, allowOverride bool) (bool, error) {
	recent, err := s.store.List(ctx, Filter{EventID: eventID, UserID: actor, Limit: 20})
	if err != nil {
		return false, err
	}
	for _, f := range recent { // newest first
		switch {
		case f.Action == ActionUndo:
			return false, nil // the latest always/never was already undone
		case f.OverrideID == "":
			continue // opened, dismissed and replies have nothing to revert
		case !allowOverride || s.ov == nil:
			return false, nil
		}
		if err := s.ov.Delete(ctx, f.OverrideID); err != nil && !errors.Is(err, overrides.ErrNotFound) {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func overrideFor(evt event.Event, action, scope, actor string) overrides.Override {
	o := overrides.Override{Mode: action, CreatedBy: actor}
	switch scope {
	case ScopeRepository:
		o.Repository = evt.Repository
	case ScopeSource:
		o.Source = evt.Source
	default:
		o.Source, o.EventType = evt.Source, evt.EventType
	}
	return o
}

func validateInput(in Input) error {
	switch in.Action {
	case ActionOpened, ActionDismissed, ActionUndo, ActionAlways, ActionNever, ActionReply:
	default:
		return fmt.Errorf("%w: action must be opened, dismissed, undo, always, never or reply", ErrInvalid)
	}
	if in.EventID == "" {
		return fmt.Errorf("%w: event id is required", ErrInvalid)
	}
	switch in.Scope {
	case "", ScopeType, ScopeRepository, ScopeSource:
	default:
		return fmt.Errorf("%w: scope must be type, repository or source", ErrInvalid)
	}
	if in.Scope != "" && in.Action != ActionAlways && in.Action != ActionNever {
		return fmt.Errorf("%w: scope only applies to always and never", ErrInvalid)
	}
	if in.Action == ActionReply && strings.TrimSpace(in.Reason) == "" {
		return fmt.Errorf("%w: a reply needs text", ErrInvalid)
	}
	if len(in.Reason) > MaxReasonLength {
		return fmt.Errorf("%w: text must be at most %d characters", ErrInvalid, MaxReasonLength)
	}
	return nil
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
