package event

type DecisionState struct {
	Status       string  `json:"status"`
	Reason       string  `json:"reason"`
	ModelVersion string  `json:"modelVersion,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
	MatchedRule  string  `json:"matchedRule,omitempty"`
}

type Event struct {
	ID             string                 `json:"id"`
	Source         string                 `json:"source"`
	EventType      string                 `json:"eventType"`
	Repository     string                 `json:"repository,omitempty"`
	AttentionClass string                 `json:"attentionClass"`
	DecisionState  *DecisionState         `json:"decisionState,omitempty"`
	DedupKey       string                 `json:"dedupKey,omitempty"`
	ThreadID       string                 `json:"threadId,omitempty"`
	MergedInto     string                 `json:"mergedInto,omitempty"`
	Payload        map[string]interface{} `json:"payload"`
}

const (
	AttentionCritical   = "critical"
	AttentionStandard   = "standard"
	AttentionBackground = "background"
)

const (
	DecisionSent           = "sent"
	DecisionQueued         = "queued"
	DecisionMerged         = "merged"
	DecisionDigest         = "digest"
	DecisionDelayed        = "delayed"
	DecisionBudgetExceeded = "budget_exceeded"
)
