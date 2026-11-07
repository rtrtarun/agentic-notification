//go:build integration

package feedback

import (
	"testing"

	"github.com/example/notification-service/pkg/db/dbtest"
)

// TestPostgresStoreContract runs the same checks as the memory store against a real database.
func TestPostgresStoreContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "feedback")
	storeContract(t, NewPostgresStore(conn))
}
