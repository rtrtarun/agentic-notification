// Package attention is Layer 2, Attention Intelligence: the only place where
// judgement about a notification (as opposed to a rule) lives. It classifies
// standard and background events, summarises groups of them, writes digests
// and explains decisions in plain language.
//
// Three promises hold for every implementation, including any model added later:
//
//   - A critical event never reaches it. Guarded refuses critical events, and
//     the pipeline never calls it for one.
//   - It can only move an event between standard and background. It cannot
//     escalate to critical, and Guarded rejects an answer that tries.
//   - The service works without it. The default implementation, Heuristic, is
//     deterministic and explains itself; a model is an optional replacement
//     behind the same interfaces.
package attention

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/example/notification-service/pkg/event"
)

// ModelHeuristic names the built-in classifier in an event's decision.
const ModelHeuristic = "heuristic-v1"

// ErrCritical is returned when something asks Attention Intelligence to handle a critical event.
var ErrCritical = errors.New("attention intelligence never handles critical events")

// Classification is one judgement about a non-critical event.
type Classification struct {
	// Class is standard or background, never critical.
	Class string
	// Score is 0 (pure noise) to 100 (needs attention soon).
	Score int
	// Confidence is 0 to 1.
	Confidence float64
	// Reasons are short phrases, in the order they were weighed.
	Reasons []string
	// Model names what decided, for the audit trail.
	Model string
}

// Reason is one sentence for the event's decision.
func (c Classification) Reason() string {
	if len(c.Reasons) == 0 {
		return fmt.Sprintf("scored %d", c.Score)
	}
	return fmt.Sprintf("scored %d: %s", c.Score, strings.Join(c.Reasons, ", "))
}

// Classifier judges one standard or background event.
type Classifier interface {
	Classify(ctx context.Context, evt event.Event) (Classification, error)
}

// Group is a set of events that belong together in a digest.
type Group struct {
	Key      string
	Title    string
	Summary  string
	Count    int
	EventIDs []string
}

// Summary describes a set of events in a few sentences, grouped.
type Summary struct {
	Title  string
	Text   string
	Groups []Group
	// Model names what wrote it; empty means the built-in heuristic.
	Model string
}

// Summarizer describes a set of non-critical events.
type Summarizer interface {
	Summarize(ctx context.Context, events []event.Event) (Summary, error)
}

// Explainer says in plain language why an event was handled the way it was.
type Explainer interface {
	Explain(ctx context.Context, evt event.Event) (string, error)
}

// SourcedExplainer is an Explainer that also says what wrote the explanation.
type SourcedExplainer interface {
	ExplainSourced(ctx context.Context, evt event.Event) (text, model string, err error)
}

// SourceOf says whether a model name means the built-in heuristic or a language model.
func SourceOf(model string) string {
	if model == "" || model == ModelHeuristic {
		return "heuristic"
	}
	return "model"
}

// Guarded wraps a Classifier so it can never see a critical event and can
// never answer with anything but standard or background. Every classifier the
// pipeline uses goes through it, so a future model cannot break the promise.
func Guarded(c Classifier) Classifier {
	if c == nil {
		return nil
	}
	return guarded{c}
}

type guarded struct{ inner Classifier }

func (g guarded) Classify(ctx context.Context, evt event.Event) (Classification, error) {
	if evt.AttentionClass == event.AttentionCritical {
		return Classification{}, ErrCritical
	}
	out, err := g.inner.Classify(ctx, evt)
	if err != nil {
		return Classification{}, err
	}
	if out.Class != event.AttentionStandard && out.Class != event.AttentionBackground {
		return Classification{}, fmt.Errorf("classifier returned %q; only standard or background are allowed", out.Class)
	}
	if out.Score < 0 || out.Score > 100 || out.Confidence < 0 || out.Confidence > 1 {
		return Classification{}, errors.New("classifier returned a score or confidence out of range")
	}
	return out, nil
}
