package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// lifecycleContract drives the dispatcher over any Store through retry with
// backoff, the dead-letter state, a manual retry, lease recovery and competing
// workers. It runs against the memory store always and against PostgreSQL in
// the integration test, where SKIP LOCKED is what keeps workers apart.
func lifecycleContract(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("retries back off, then dead-letter, then a person retries it", func(t *testing.T) {
		p := &scriptedProvider{errs: []error{errors.New("down"), errors.New("down"), errors.New("down")}}
		targets := Targets{Slack: []string{"https://hooks.slack.com/services/SECRET"}}
		clk := &clock{t: t0}
		out := NewOutbox(store, targets, map[string][]string{"standard": {ChannelSlack}})
		out.now = clk.now
		d := NewDispatcher(store, targets, map[string]Provider{ChannelSlack: p}, nil).WithClock(clk.now).WithRetryPolicy(3, time.Minute)

		if n, err := out.Enqueue(ctx, msg("life-1", "standard")); err != nil || n != 1 {
			t.Fatalf("enqueue = %d, %v", n, err)
		}
		d.RunOnce(ctx) // attempt 1 fails, retry in 1m
		if n, _ := d.RunOnce(ctx); n != 0 {
			t.Fatal("retried before the backoff elapsed")
		}
		clk.t = clk.t.Add(time.Minute)
		d.RunOnce(ctx) // attempt 2 fails, retry in 2m
		clk.t = clk.t.Add(time.Minute)
		if n, _ := d.RunOnce(ctx); n != 0 {
			t.Fatal("the backoff must double")
		}
		clk.t = clk.t.Add(time.Minute)
		d.RunOnce(ctx) // attempt 3 fails: dead

		job := jobFor(t, store, "life-1")
		if job.Status != StatusDead || job.Attempts != 3 || job.LastError != "down" {
			t.Fatalf("after three failures: %+v", job)
		}
		if err := store.Requeue(ctx, job.ID, clk.t); err != nil {
			t.Fatal(err)
		}
		if again := jobFor(t, store, "life-1"); again.Status != StatusPending || again.Attempts != 0 {
			t.Fatalf("requeue must reset the job: %+v", again)
		}
		d.RunOnce(ctx)
		if done := jobFor(t, store, "life-1"); done.Status != StatusSent {
			t.Fatalf("after a manual retry: %+v", done)
		}
		if err := store.Requeue(ctx, job.ID, clk.t); !errors.Is(err, ErrNotRetryable) {
			t.Fatalf("a sent job must not be retryable, got %v", err)
		}
	})

	t.Run("a worker that dies mid-send loses its lease and the job is picked up again", func(t *testing.T) {
		ok, err := store.Enqueue(ctx, job("life-lease", ChannelSlack))
		if err != nil || !ok {
			t.Fatalf("enqueue: %v %v", ok, err)
		}
		first, err := store.Claim(ctx, t0, time.Minute, 50)
		if err != nil || !hasEvent(first, "life-lease") {
			t.Fatalf("first claim: %+v %v", first, err)
		}
		if again, _ := store.Claim(ctx, t0.Add(30*time.Second), time.Minute, 50); hasEvent(again, "life-lease") {
			t.Fatal("a leased job was handed out twice")
		}
		later, _ := store.Claim(ctx, t0.Add(2*time.Minute), time.Minute, 50)
		if !hasEvent(later, "life-lease") {
			t.Fatal("an expired lease must make the job claimable again")
		}
		for _, j := range later {
			if j.EventID == "life-lease" && j.Attempts != 2 {
				t.Fatalf("the second claim must count a second attempt, got %d", j.Attempts)
			}
		}
	})

	t.Run("competing workers never claim the same job", func(t *testing.T) {
		const jobs = 30
		for i := 0; i < jobs; i++ {
			if _, err := store.Enqueue(ctx, job(fmt.Sprintf("life-race-%d", i), ChannelSlack)); err != nil {
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
					got, err := store.Claim(ctx, t0.Add(10*time.Minute), time.Hour, 5)
					if err != nil {
						t.Errorf("claim: %v", err)
						return
					}
					if len(got) == 0 {
						return
					}
					mu.Lock()
					for _, j := range got {
						claimed[j.ID]++
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		raced := 0
		for id, n := range claimed {
			if n != 1 {
				t.Fatalf("job %s was claimed %d times", id, n)
			}
		}
		for i := 0; i < jobs; i++ {
			list, _ := store.List(ctx, 1000)
			for _, j := range list {
				if j.EventID == fmt.Sprintf("life-race-%d", i) && claimed[j.ID] == 1 {
					raced++
				}
			}
		}
		if raced != jobs {
			t.Fatalf("every job must be claimed exactly once, got %d of %d", raced, jobs)
		}
	})
}

func jobFor(t *testing.T, s Store, eventID string) Job {
	t.Helper()
	list, err := s.List(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range list {
		if j.EventID == eventID {
			return j
		}
	}
	t.Fatalf("no job for event %s", eventID)
	return Job{}
}

func hasEvent(jobs []Job, eventID string) bool {
	for _, j := range jobs {
		if j.EventID == eventID {
			return true
		}
	}
	return false
}

func TestMemoryStoreLifecycle(t *testing.T) { lifecycleContract(t, NewMemoryStore()) }
