package learning

import (
	"context"
	"errors"
	"time"
)

// Statuses of a score adjustment.
const (
	// AdjustmentProposed is waiting for an admin. It changes nothing.
	AdjustmentProposed = "proposed"
	// AdjustmentActive was approved by an admin and is in force.
	AdjustmentActive = "active"
	// AdjustmentRejected was refused by an admin and is not proposed again.
	AdjustmentRejected = "rejected"
	// AdjustmentRetired was active and was switched off by an admin.
	AdjustmentRetired = "retired"
)

// Statuses of a learning run.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
)

// Defaults for TunerConfig, fixed by the owner for milestone 38.
const (
	DefaultMinPeople     = 2
	DefaultMaxAdjustment = 20
	DefaultHalfLife      = 14 * 24 * time.Hour
	// PerPersonCap bounds how much one person's (decayed) signals can count for or against a scope.
	PerPersonCap = 3.0
	// smoothing keeps small evidence small: offset = max × (P−N) / (P+N+smoothing).
	smoothing = 2.0
	// StaleRunAfter is how long a run may stay "running" before it is treated as abandoned.
	StaleRunAfter = 10 * time.Minute
)

// Errors the HTTP layer maps to status codes.
var (
	ErrAdjustmentNotFound = errors.New("adjustment not found")
	ErrNotProposed        = errors.New("adjustment is no longer proposed")
	ErrNotActive          = errors.New("adjustment is not active")
	// ErrTuneGuardrail is returned when an adjustment would touch critical or locked routing.
	ErrTuneGuardrail = errors.New("critical and locked rules can never be adjusted by learning")
	ErrRunInProgress = errors.New("a learning run is already in progress")
)

// TuneEvidence says what people did, in counts, with a plain-language summary.
type TuneEvidence struct {
	Positive   int    `json:"positive"`
	Negative   int    `json:"negative"`
	People     int    `json:"people"`
	Signals    int    `json:"signals"`
	WindowDays int    `json:"windowDays"`
	Summary    string `json:"summary"`
}

// Impact is the shadow estimate: recent events replayed through the heuristic
// with and without the adjustment. Nothing is changed by computing it.
type Impact struct {
	Replayed    int `json:"replayed"`
	WouldChange int `json:"wouldChange"`
}

// Adjustment is one proposed or approved score offset for a kind of event.
type Adjustment struct {
	ID         string       `json:"id"`
	Source     string       `json:"source"`
	EventType  string       `json:"eventType"`
	Repository string       `json:"repository,omitempty"`
	Delta      int          `json:"delta"`
	Status     string       `json:"status"`
	Evidence   TuneEvidence `json:"evidence"`
	Impact     Impact       `json:"impact"`
	// Version is the version of the active set when this adjustment last changed it (approved or retired); 0 until then.
	Version    int        `json:"version"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ResolvedBy string     `json:"resolvedBy,omitempty"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// Run is one background learning pass.
type Run struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
	FeedbackRead  int        `json:"feedbackRead"`
	EventsRead    int        `json:"eventsRead"`
	ProposalsMade int        `json:"proposalsMade"`
	Error         string     `json:"error,omitempty"`
}

// AdjustmentRepository keeps adjustments. There is one per scope (source, event type, repository).
// Implementations: MemoryAdjustments and PostgresAdjustments.
type AdjustmentRepository interface {
	// Save inserts an adjustment or replaces the one with the same ID.
	Save(ctx context.Context, a Adjustment) error
	// Get returns ErrAdjustmentNotFound when there is none.
	Get(ctx context.Context, id string) (Adjustment, error)
	// List returns adjustments with the status (all when empty), newest update first.
	List(ctx context.Context, status string) ([]Adjustment, error)
	// MaxVersion is the highest version of any adjustment, 0 when there are none.
	MaxVersion(ctx context.Context) (int, error)
}

// RunRepository keeps the log of learning runs and makes sure only one is running.
type RunRepository interface {
	// Start records a new running run. A run still marked running that started before staleBefore
	// is first marked failed as abandoned. ErrRunInProgress when another run is running.
	Start(ctx context.Context, r Run, staleBefore time.Time) error
	// Finish records the outcome of a run.
	Finish(ctx context.Context, r Run) error
	// List returns the latest runs, newest first.
	List(ctx context.Context, limit int) ([]Run, error)
}
