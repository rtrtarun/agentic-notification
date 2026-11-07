//go:build integration

package event

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentpulse/notification-service/pkg/db/dbtest"
)

// TestPostgresWorkQueue runs the work-queue contract against a real database.
func TestPostgresWorkQueue(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "events")
	workQueueContract(t, NewPostgresRepository(conn))
}

// TestPostgresCompetingWorkersNeverClaimTheSameEvent checks FOR UPDATE SKIP LOCKED.
func TestPostgresCompetingWorkersNeverClaimTheSameEvent(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "events")
	repo := NewPostgresRepository(conn)
	ctx := context.Background()
	const events = 40
	for i := 0; i < events; i++ {
		if err := repo.Save(ctx, queued("race-"+string(rune('a'+i%26))+string(rune('a'+i/26)), AttentionStandard, true)); err != nil {
			t.Fatal(err)
		}
	}
	var (
		mu      sync.Mutex
		claimed = map[string]int{}
		wg      sync.WaitGroup
	)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				got, err := repo.ClaimPending(ctx, time.Now(), time.Hour, 5)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if len(got) == 0 {
					return
				}
				mu.Lock()
				for _, e := range got {
					claimed[e.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(claimed) != events {
		t.Fatalf("claimed %d of %d events", len(claimed), events)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Fatalf("event %s claimed %d times", id, n)
		}
	}
}

// TestPostgresTraceFields runs the trace-field contract against a real database.
func TestPostgresTraceFields(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Clear(t, conn, "events")
	traceFieldsContract(t, NewPostgresRepository(conn))
}
