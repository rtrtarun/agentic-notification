//go:build integration

package preferences

import (
	"testing"

	"github.com/example/notification-service/pkg/db/dbtest"
)

// TestPostgresPersonalStoreContract runs the contract against a real database.
func TestPostgresPersonalStoreContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "user_preferences")
	personalStoreContract(t, NewPostgresPersonalStore(conn))
}
