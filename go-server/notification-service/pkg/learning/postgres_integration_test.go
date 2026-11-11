//go:build integration

package learning

import (
	"testing"

	"github.com/agentpulse/notification-service/pkg/db/dbtest"
)

// TestPostgresRepositoryContract runs the same checks as the memory repository against a real database.
func TestPostgresRepositoryContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "learning_suggestions")
	repositoryContract(t, NewPostgresRepository(conn))
}

// TestPostgresAdjustmentsContract runs the adjustment checks against a real database.
func TestPostgresAdjustmentsContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "learning_adjustments")
	adjustmentContract(t, NewPostgresAdjustments(conn))
}

// TestPostgresRunsContract runs the run-log checks, including the one-running-run index, against a real database.
func TestPostgresRunsContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "learning_runs")
	runContract(t, NewPostgresRuns(conn))
}
