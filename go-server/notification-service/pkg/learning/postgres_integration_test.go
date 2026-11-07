//go:build integration

package learning

import (
	"testing"

	"github.com/example/notification-service/pkg/db/dbtest"
)

// TestPostgresRepositoryContract runs the same checks as the memory repository against a real database.
func TestPostgresRepositoryContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "learning_suggestions")
	repositoryContract(t, NewPostgresRepository(conn))
}
