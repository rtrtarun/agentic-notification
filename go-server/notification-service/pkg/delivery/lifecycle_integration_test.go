//go:build integration

package delivery

import (
	"testing"

	"github.com/example/notification-service/pkg/db/dbtest"
)

// TestPostgresStoreLifecycle runs the retry, dead-letter, lease and competing-worker
// checks against a real database, where FOR UPDATE SKIP LOCKED keeps workers apart.
func TestPostgresStoreLifecycle(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "delivery_jobs")
	lifecycleContract(t, NewPostgresStore(conn))
}
