// Package inbox holds what a person does with events: read, archive, snooze,
// acknowledge, resolve. It sits beside the event store and never changes an
// event or its decision; the pipeline stays the only author of those.
package inbox

import (
	"errors"
	"time"

	"github.com/example/notification-service/pkg/event"
)

// Level is the priority shown to people. Four levels are a display layer over
// the three stored attention classes (see LevelOf).
type Level string

const (
	LevelCritical Level = "critical"
	LevelHigh     Level = "high"
	LevelNormal   Level = "normal"
	LevelLow      Level = "low"
)

// Outcome says what the pipeline did with an event.
type Outcome string

const (
	OutcomeDelivered  Outcome = "delivered"
	OutcomeHeld       Outcome = "held"
	OutcomeSuppressed Outcome = "suppressed"
	OutcomeMerged     Outcome = "merged"
	OutcomeRecorded   Outcome = "recorded"
)

// Errors the HTTP layer maps to status codes.
var (
	ErrNotFound = errors.New("notification not found")
	ErrInvalid  = errors.New("invalid request")
)

// State is one person's handling of one event. The zero value means untouched.
type State struct {
	EventID          string     `json:"eventId"`
	ReadAt           *time.Time `json:"readAt,omitempty"`
	ArchivedAt       *time.Time `json:"archivedAt,omitempty"`
	DeletedAt        *time.Time `json:"deletedAt,omitempty"`
	AcknowledgedAt   *time.Time `json:"acknowledgedAt,omitempty"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
	SnoozedUntil     *time.Time `json:"snoozedUntil,omitempty"`
	PriorityOverride Level      `json:"priorityOverride,omitempty"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// TrailEntry is one step in an event's life. Reconstructed entries are derived
// from what is stored about the event, not recorded when it happened.
type TrailEntry struct {
	Step          string    `json:"step"`
	Detail        string    `json:"detail,omitempty"`
	At            time.Time `json:"at"`
	Reconstructed bool      `json:"reconstructed,omitempty"`
}

// Item is one row of the inbox: an event summary plus the person's state.
type Item struct {
	ID               string               `json:"id"`
	Source           string               `json:"source"`
	EventType        string               `json:"eventType"`
	Repository       string               `json:"repository,omitempty"`
	AttentionClass   string               `json:"attentionClass"`
	Priority         Level                `json:"priority"`
	Outcome          Outcome              `json:"outcome"`
	DecisionState    *event.DecisionState `json:"decisionState,omitempty"`
	ThreadID         string               `json:"threadId,omitempty"`
	MergedInto       string               `json:"mergedInto,omitempty"`
	RepeatCount      int                  `json:"repeatCount"`
	CreatedAt        time.Time            `json:"createdAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
	Read             bool                 `json:"read"`
	ReadAt           *time.Time           `json:"readAt,omitempty"`
	ArchivedAt       *time.Time           `json:"archivedAt,omitempty"`
	AcknowledgedAt   *time.Time           `json:"acknowledgedAt,omitempty"`
	ResolvedAt       *time.Time           `json:"resolvedAt,omitempty"`
	SnoozedUntil     *time.Time           `json:"snoozedUntil,omitempty"`
	PriorityOverride Level                `json:"priorityOverride,omitempty"`
}

// Detail is everything the drawer shows for one notification.
type Detail struct {
	Item    Item         `json:"item"`
	Trail   []TrailEntry `json:"trail"`
	Related []Item       `json:"related"`
}

// Counts are the numbers on the inbox tabs. Archived items count only under Archived.
type Counts struct {
	All        int `json:"all"`
	Unread     int `json:"unread"`
	Critical   int `json:"critical"`
	High       int `json:"high"`
	Normal     int `json:"normal"`
	Low        int `json:"low"`
	Held       int `json:"held"`
	Delivered  int `json:"delivered"`
	Suppressed int `json:"suppressed"`
	Archived   int `json:"archived"`
}

// Page is one page of items with the totals the interface needs.
type Page struct {
	Items    []Item `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
	Counts   Counts `json:"counts"`
}

// Sort orders.
const (
	SortNewest   = "newest"
	SortOldest   = "oldest"
	SortPriority = "priority"
	SortLowest   = "lowest"
	SortUpdated  = "updated"
)

// Paging limits.
const (
	DefaultPageSize = 25
	MaxPageSize     = 100
)

// Query selects and orders inbox items. Empty fields do not filter.
type Query struct {
	Text       string
	Priorities []Level
	Outcomes   []Outcome
	Read       *bool
	Archived   bool
	Source     string
	Repository string
	From, To   *time.Time
	Sort       string
	Page       int
	PageSize   int
}

// ActionType is something a person can do to a notification.
type ActionType string

const (
	ActionRead        ActionType = "read"
	ActionUnread      ActionType = "unread"
	ActionArchive     ActionType = "archive"
	ActionUnarchive   ActionType = "unarchive"
	ActionDelete      ActionType = "delete"
	ActionAcknowledge ActionType = "acknowledge"
	ActionResolve     ActionType = "resolve"
	ActionSnooze      ActionType = "snooze"
	ActionUnsnooze    ActionType = "unsnooze"
	ActionPriority    ActionType = "priority"
)

// Action is one request to change a notification.
type Action struct {
	Type     ActionType
	Until    time.Time // snooze only
	Priority Level     // priority only; empty clears the override
}

// BulkResult reports what happened to one id in a bulk request.
type BulkResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}
