package event

import "time"

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
	// CreatedAt is when the event was stored. Set by the repository.
	CreatedAt time.Time `json:"createdAt,omitempty"`
	// AttentionPending is set by the pipeline when it saves a non-critical event that the
	// asynchronous attention worker must still decide on. It is not part of the API.
	AttentionPending bool `json:"-"`
	// RouteChannels is the channel override of the rule that matched, set while the pipeline
	// decides. It is not stored: the worker reads it again from the current rule.
	RouteChannels []string `json:"-"`
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
