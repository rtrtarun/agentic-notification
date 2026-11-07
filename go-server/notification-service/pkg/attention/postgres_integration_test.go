//go:build integration

package attention

import (
	"testing"

	"github.com/example/notification-service/pkg/db/dbtest"
)

// TestPostgresDigestStoreContract runs the same checks as the memory store against a real database.
func TestPostgresDigestStoreContract(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "digests")
	digestStoreContract(t, NewPostgresDigestStore(conn))
}
