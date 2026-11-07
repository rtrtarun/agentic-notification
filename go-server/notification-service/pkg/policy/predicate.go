package policy

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/example/notification-service/pkg/event"
)

// Predicate operators.
const (
	OpEq     = "eq"
	OpNe     = "ne"
	OpGt     = "gt"
	OpGte    = "gte"
	OpLt     = "lt"
	OpLte    = "lte"
	OpExists = "exists"
)

const (
	// MaxPredicates bounds how many payload predicates one rule may carry.
	MaxPredicates = 5
	maxFieldLen   = 100
	maxValueLen   = 200
)

var fieldPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-]*$`)

// KnownChannels are the delivery channels a rule may route to. A test in pkg/delivery
// keeps this list identical to the channels the outbox knows.
var KnownChannels = []string{"email", "slack", "teams", "sms"}

// Predicate is one condition on the contents of an event, such as
// "metadata.attempt gt 3" or "severity eq high". All predicates of a rule must hold.
//
// Field is a dotted path into the event payload (for example metadata.attempt or
// pull_request.draft), or one of the normalised names branch, author and severity,
// which look in the places each source puts them. A field the event does not have
// never matches, except under "exists" with value false.
type Predicate struct {
	Field string `json:"field" yaml:"field"`
	// Op is eq (the default), ne, gt, gte, lt, lte or exists.
	Op string `json:"op,omitempty" yaml:"op,omitempty"`
	// Value is a string, number or boolean. For exists it is optional: true (the default) or false.
	Value any `json:"value,omitempty" yaml:"value,omitempty"`
}

// Resolve finds a field in an event. The second result is false when the event lacks it.
func Resolve(evt event.Event, field string) (any, bool) {
	switch field {
	case "branch":
		if v, ok := firstString(evt.Payload, []string{"branch"}, []string{"pull_request", "head", "ref"}, []string{"ref"}); ok {
			return strings.TrimPrefix(v, "refs/heads/"), true
		}
		return nil, false
	case "author":
		return firstValue(evt.Payload, []string{"author"}, []string{"sender", "login"}, []string{"pusher", "name"}, []string{"metadata", "author"})
	case "severity":
		return firstValue(evt.Payload, []string{"severity"}, []string{"metadata", "severity"}, []string{"alert", "severity"}, []string{"security_advisory", "severity"})
	}
	return lookup(evt.Payload, strings.Split(field, "."))
}

func firstValue(m map[string]any, paths ...[]string) (any, bool) {
	for _, p := range paths {
		if v, ok := lookup(m, p); ok {
			return v, true
		}
	}
	return nil, false
}

func firstString(m map[string]any, paths ...[]string) (string, bool) {
	v, ok := firstValue(m, paths...)
	if !ok {
		return "", false
	}
	s, isString := v.(string)
	return s, isString
}

func lookup(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = next[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// matchPredicates reports whether the event satisfies every predicate.
func matchPredicates(preds []Predicate, evt event.Event) bool {
	for _, p := range preds {
		if !p.matches(evt) {
			return false
		}
	}
	return true
}

func (p Predicate) matches(evt event.Event) bool {
	got, found := Resolve(evt, p.Field)
	op := p.Op
	if op == "" {
		op = OpEq
	}
	if op == OpExists {
		want := true
		if b, ok := p.Value.(bool); ok {
			want = b
		}
		return found == want
	}
	if !found {
		return false
	}
	switch op {
	case OpEq:
		return equal(got, p.Value)
	case OpNe:
		return !equal(got, p.Value)
	}
	a, aok := number(got)
	b, bok := number(p.Value)
	if !aok || !bok {
		return false
	}
	switch op {
	case OpGt:
		return a > b
	case OpGte:
		return a >= b
	case OpLt:
		return a < b
	case OpLte:
		return a <= b
	}
	return false
}

func equal(got, want any) bool {
	if a, ok := number(got); ok {
		if b, ok := number(want); ok {
			return a == b
		}
	}
	if a, ok := got.(bool); ok {
		b, ok := want.(bool)
		return ok && a == b
	}
	a, aok := got.(string)
	b, bok := want.(string)
	return aok && bok && strings.EqualFold(a, b)
}

// number reads a value as a float. Numeric strings count, because many senders put numbers in strings.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n)
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// ValidatePredicates checks predicates before a rule is stored, so a typo fails
// when the rule is saved rather than silently never matching.
func ValidatePredicates(preds []Predicate) error {
	if len(preds) > MaxPredicates {
		return fmt.Errorf("a rule can have at most %d payload predicates", MaxPredicates)
	}
	for i, p := range preds {
		if p.Field == "" || utf8.RuneCountInString(p.Field) > maxFieldLen || !fieldPattern.MatchString(p.Field) {
			return fmt.Errorf("payload predicate %d: field must be a dotted name such as metadata.attempt", i+1)
		}
		switch p.Op {
		case "", OpEq, OpNe:
			if !scalar(p.Value) {
				return fmt.Errorf("payload predicate %d: %s needs a string, number or true/false value", i+1, orEq(p.Op))
			}
		case OpGt, OpGte, OpLt, OpLte:
			if _, ok := number(p.Value); !ok {
				return fmt.Errorf("payload predicate %d: %s needs a number", i+1, p.Op)
			}
		case OpExists:
			if _, isBool := p.Value.(bool); p.Value != nil && !isBool {
				return fmt.Errorf("payload predicate %d: exists takes true or false", i+1)
			}
		default:
			return fmt.Errorf("payload predicate %d: unknown operator %q (use eq, ne, gt, gte, lt, lte or exists)", i+1, p.Op)
		}
		if s, ok := p.Value.(string); ok && utf8.RuneCountInString(s) > maxValueLen {
			return fmt.Errorf("payload predicate %d: value is at most %d characters", i+1, maxValueLen)
		}
	}
	return nil
}

func orEq(op string) string {
	if op == "" {
		return OpEq
	}
	return op
}

func scalar(v any) bool {
	switch v.(type) {
	case string, bool, float64, float32, int, int32, int64, uint64:
		return true
	}
	return false
}

// ValidateChannels checks a rule's channel override: known names only, no repeats,
// and never on a critical rule. Critical events always go to every configured
// channel, so a rule cannot narrow where one is sent.
func ValidateChannels(class string, channels []string) error {
	if len(channels) == 0 {
		return nil
	}
	if class == event.AttentionCritical {
		return fmt.Errorf("a critical rule cannot choose channels; critical events always go to every configured channel")
	}
	seen := map[string]bool{}
	for _, c := range channels {
		known := false
		for _, k := range KnownChannels {
			known = known || k == c
		}
		if !known {
			return fmt.Errorf("unknown channel %q (use email, slack, teams or sms)", c)
		}
		if seen[c] {
			return fmt.Errorf("channel %q is listed twice", c)
		}
		seen[c] = true
	}
	return nil
}
