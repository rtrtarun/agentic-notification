// Package agentevents turns what an AI agent reports about one step of its run into
// the platform's event. It is deterministic Layer 1 input handling: it validates and
// normalises, and decides nothing about importance.
//
// In particular an agent's own "severity" is only data. It lands in the payload where
// rules can match on it, but it never sets the attention class: only a rule does that,
// so an agent cannot declare its own event critical or talk its way out of a rule.
package agentevents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agentpulse/notification-service/pkg/event"
	"github.com/agentpulse/notification-service/pkg/traces"
)

// DefaultSource is used when an agent does not name one.
const DefaultSource = "agent"

// ErrInvalid wraps every validation failure; its message is safe to show.
var ErrInvalid = errors.New("invalid agent event")

const (
	maxName        = 100
	maxID          = 200
	maxMetadata    = 30
	maxStringValue = 500
)

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:\-]*$`)
	typePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.\-]*$`)
	srcPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.\-]*$`)

	stepKinds  = map[string]bool{traces.StepPlan: true, traces.StepToolCall: true, traces.StepRetry: true, traces.StepReflect: true, traces.StepHumanApproval: true, traces.StepComplete: true}
	severities = map[string]bool{"info": true, "low": true, "medium": true, "high": true, "critical": true}
	// nonNegative are the telemetry numbers that cannot be below zero.
	nonNegative = []string{"attempt", "duration_ms", "tokens_in", "tokens_out", "cost_usd"}
)

// Input is one step as an agent reports it.
type Input struct {
	// ID makes the call idempotent: sending the same ID twice stores one event. Optional.
	ID            string         `json:"id"`
	Source        string         `json:"source"`
	EventType     string         `json:"eventType"`
	Repository    string         `json:"repository"`
	TraceID       string         `json:"traceId"`
	ParentEventID string         `json:"parentEventId"`
	AgentName     string         `json:"agentName"`
	Severity      string         `json:"severity"`
	Metadata      map[string]any `json:"metadata"`
	Payload       map[string]any `json:"payload"`
}

// Normalize validates an input and returns the event to hand to the pipeline. The
// event's class is left empty: the pipeline's rules decide it.
func Normalize(in Input) (event.Event, error) {
	fail := func(format string, args ...any) (event.Event, error) {
		return event.Event{}, fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	in.Source = strings.ToLower(strings.TrimSpace(in.Source))
	if in.Source == "" {
		in.Source = DefaultSource
	}
	if !srcPattern.MatchString(in.Source) || len(in.Source) > 50 {
		return fail("source must be lower-case letters, digits, dots, dashes or underscores")
	}
	in.EventType = strings.TrimSpace(in.EventType)
	if in.EventType == "" || len(in.EventType) > maxName || !typePattern.MatchString(in.EventType) {
		return fail("eventType is required, such as tool.call.failed")
	}
	for name, v := range map[string]string{"id": in.ID, "traceId": in.TraceID, "parentEventId": in.ParentEventID} {
		if v != "" && (len(v) > maxID || !idPattern.MatchString(v)) {
			return fail("%s may only contain letters, digits and . _ : - (at most %d characters)", name, maxID)
		}
	}
	if in.ParentEventID != "" && in.TraceID == "" {
		return fail("parentEventId needs a traceId")
	}
	if in.ParentEventID != "" && in.ParentEventID == in.ID {
		return fail("an event cannot be its own parent")
	}
	if utf8.RuneCountInString(in.AgentName) > maxName || utf8.RuneCountInString(in.Repository) > maxName*2 {
		return fail("agentName and repository are too long")
	}
	in.Severity = strings.ToLower(strings.TrimSpace(in.Severity))
	if in.Severity != "" && !severities[in.Severity] {
		return fail("severity must be info, low, medium, high or critical")
	}
	if err := checkMetadata(in.Metadata); err != nil {
		return fail("%s", err.Error())
	}

	payload := make(map[string]any, len(in.Payload)+3)
	for k, v := range in.Payload {
		payload[k] = v
	}
	if len(in.Metadata) > 0 {
		payload["metadata"] = copyMap(in.Metadata)
	}
	if in.Severity != "" {
		payload["severity"] = in.Severity // data for rules; it never sets the class
	}
	if in.AgentName != "" {
		payload["agentName"] = in.AgentName
	}
	id := in.ID
	if id == "" {
		var b [10]byte
		if _, err := rand.Read(b[:]); err != nil {
			return event.Event{}, fmt.Errorf("generate event id: %w", err)
		}
		id = DefaultSource + "-" + hex.EncodeToString(b[:])
	}
	return event.Event{
		ID: id, Source: in.Source, EventType: in.EventType, Repository: in.Repository,
		TraceID: in.TraceID, ParentEventID: in.ParentEventID, AgentName: in.AgentName, Payload: payload,
	}, nil
}

func checkMetadata(md map[string]any) error {
	if len(md) > maxMetadata {
		return fmt.Errorf("metadata may have at most %d keys", maxMetadata)
	}
	if kind, ok := md["step_kind"]; ok {
		s, isString := kind.(string)
		if !isString || !stepKinds[s] {
			return errors.New("metadata.step_kind must be plan, tool_call, retry, reflect, human_approval or complete")
		}
	}
	for _, key := range nonNegative {
		if v, ok := md[key]; ok {
			n, isNumber := number(v)
			if !isNumber || n < 0 {
				return fmt.Errorf("metadata.%s must be a number of zero or more", key)
			}
		}
	}
	if v, ok := md["confidence"]; ok {
		n, isNumber := number(v)
		if !isNumber || n < 0 || n > 1 {
			return errors.New("metadata.confidence must be a number from 0 to 1")
		}
	}
	for k, v := range md {
		if s, ok := v.(string); ok && utf8.RuneCountInString(s) > maxStringValue {
			return fmt.Errorf("metadata.%s is longer than %d characters", k, maxStringValue)
		}
	}
	return nil
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
