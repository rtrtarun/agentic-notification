package policy

import (
	"path"
	"sync"

	"github.com/example/notification-service/pkg/event"
)

// Engine evaluates rules in order. It is safe for concurrent use, and its rules
// can be replaced while the service runs.
type Engine struct {
	mu    sync.RWMutex
	rules []Rule
}

func NewEngine(rules []Rule) *Engine {
	return &Engine{rules: append([]Rule(nil), rules...)}
}

// SetRules replaces the rules. Evaluations already running keep the old list.
func (e *Engine) SetRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append([]Rule(nil), rules...)
}

func (e *Engine) snapshot() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rules
}

func (e *Engine) Evaluate(evt event.Event) (PolicyDecision, bool) {
	for _, rule := range e.snapshot() {
		if rule.Disabled {
			continue
		}
		if Matches(rule.When, evt) {
			return PolicyDecision{
				AttentionClass: rule.Then.AttentionClass,
				Reason:         rule.Then.Reason,
				MatchedRule:    rule.Name,
				Adaptive:       rule.Then.Adaptive && rule.Then.AttentionClass != event.AttentionCritical,
			}, true
		}
	}
	return PolicyDecision{}, false
}

// Matches reports whether the event satisfies every set field of the condition.
func Matches(c RuleCondition, evt event.Event) bool {
	return fieldMatches(c.Source, evt.Source) &&
		fieldMatches(c.EventType, evt.EventType) &&
		fieldMatches(c.Repository, evt.Repository)
}

// fieldMatches treats an empty pattern as "anything" and otherwise matches with
// path.Match, so * is a wildcard that does not cross a slash: "acme/*" matches
// "acme/api" but not "acme/api/v2".
func fieldMatches(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	ok, err := path.Match(pattern, value)
	if err != nil {
		// A malformed pattern never matches; ValidatePattern rejects it at the API.
		return pattern == value
	}
	return ok
}

// ValidatePattern reports whether a condition field is a usable pattern.
func ValidatePattern(pattern string) error {
	_, err := path.Match(pattern, "")
	return err
}
