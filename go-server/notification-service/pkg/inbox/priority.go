package inbox

import "github.com/agentpulse/notification-service/pkg/event"

// LevelOf maps the stored attention class to the four levels people see.
//
//	critical   -> Critical, always; nothing can lower it.
//	standard   -> High when the pipeline sent it to you, otherwise Normal.
//	background -> Low.
//
// A person's override applies only to events that are not critical.
func LevelOf(e event.Event, s State) Level {
	if e.AttentionClass == event.AttentionCritical {
		return LevelCritical
	}
	if s.PriorityOverride != "" {
		return s.PriorityOverride
	}
	if e.AttentionClass == event.AttentionBackground {
		return LevelLow
	}
	if status(e) == event.DecisionSent {
		return LevelHigh
	}
	return LevelNormal
}

// OutcomeOf names what the pipeline did with the event.
func OutcomeOf(e event.Event) Outcome {
	if e.MergedInto != "" {
		return OutcomeMerged
	}
	switch status(e) {
	case event.DecisionMerged:
		return OutcomeMerged
	case event.DecisionSent:
		return OutcomeDelivered
	case event.DecisionQueued, event.DecisionDelayed, event.DecisionDigest:
		return OutcomeHeld
	case event.DecisionBudgetExceeded:
		return OutcomeSuppressed
	default:
		return OutcomeRecorded
	}
}

func status(e event.Event) string {
	if e.DecisionState == nil {
		return ""
	}
	return e.DecisionState.Status
}

func levelRank(l Level) int {
	switch l {
	case LevelCritical:
		return 0
	case LevelHigh:
		return 1
	case LevelNormal:
		return 2
	default:
		return 3
	}
}

// ValidLevel reports whether l is one of the four levels.
func ValidLevel(l Level) bool {
	return l == LevelCritical || l == LevelHigh || l == LevelNormal || l == LevelLow
}
