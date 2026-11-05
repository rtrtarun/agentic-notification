package delivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func job(event, channel string) Job {
	return Job{
		ID: NewJobID(), EventID: event, Channel: channel, Class: "standard", Title: "t", Message: "m",
		Status: StatusPending, NextAttemptAt: t0, CreatedAt: t0, UpdatedAt: t0,
	}
}

// storeContract is shared by every Store implementation.
func storeContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	ok, err := s.Enqueue(ctx, job("e1", ChannelSlack))
	if err != nil || !ok {
		t.Fatalf("first enqueue = %v, %v", ok, err)
	}
	if ok, _ := s.Enqueue(ctx, job("e1", ChannelSlack)); ok {
		t.Fatal("the same event and channel must not be queued twice")
	}
	if ok, _ := s.Enqueue(ctx, job("e1", ChannelEmail)); !ok {
		t.Fatal("another channel for the same event is a separate job")
	}

	if got, _ := s.Claim(ctx, t0.Add(-time.Hour), time.Minute, 10); len(got) != 0 {
		t.Fatalf("a job that is not due was claimed: %v", got)
	}
	claimed, err := s.Claim(ctx, t0, time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 || claimed[0].Status != StatusSending {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	rest, _ := s.Claim(ctx, t0, time.Minute, 10)
	if len(rest) != 1 || rest[0].ID == claimed[0].ID {
		t.Fatalf("a leased job was claimed again: %+v", rest)
	}

	// A lease that expired makes the job claimable again, with the attempt counted.
	again, _ := s.Claim(ctx, t0.Add(2*time.Minute), time.Minute, 10)
	if len(again) != 2 {
		t.Fatalf("expired leases = %+v", again)
	}
	for _, j := range again {
		if j.Attempts != 2 { // each job was claimed once before and is claimed again now
			t.Fatalf("job %s attempts = %d, want 2", j.ID, j.Attempts)
		}
	}

	id := claimed[0].ID
	retryAt := t0.Add(time.Hour)
	if err := s.Fail(ctx, id, "boom", &retryAt, t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Claim(ctx, t0.Add(30*time.Minute), time.Minute, 10); containsID(got, id) {
		t.Fatal("a job waiting for its retry time was claimed early")
	}
	if got, _ := s.Claim(ctx, retryAt, time.Minute, 10); !containsID(got, id) {
		t.Fatal("a job past its retry time was not claimed")
	}

	if err := s.Fail(ctx, id, "gave up", nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, id, t0); err != nil {
		t.Fatalf("requeue dead: %v", err)
	}
	if err := s.Requeue(ctx, id, t0); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("requeue of a pending job = %v", err)
	}
	if err := s.Requeue(ctx, "missing", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requeue of a missing job = %v", err)
	}
	if err := s.Complete(ctx, "missing", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("complete of a missing job = %v", err)
	}

	if err := s.Complete(ctx, id, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountSent(ctx, claimed[0].Channel, t0.Add(-time.Minute)); n != 1 {
		t.Fatalf("CountSent = %d", n)
	}
	if n, _ := s.CountSent(ctx, claimed[0].Channel, t0.Add(time.Minute)); n != 0 {
		t.Fatalf("CountSent after the window = %d", n)
	}
	counts, _ := s.Counts(ctx)
	if counts[StatusSent] != 1 {
		t.Fatalf("counts = %v", counts)
	}

	// Record replaces the log entry for the same event and channel.
	direct := job("e2", "email+slack")
	direct.Direct, direct.Status = true, StatusDead
	if err := s.Record(ctx, direct); err != nil {
		t.Fatal(err)
	}
	direct.ID, direct.Status = NewJobID(), StatusSent
	if err := s.Record(ctx, direct); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx, 50)
	n := 0
	for _, j := range list {
		if j.EventID == "e2" {
			n++
			if j.Status != StatusSent {
				t.Fatalf("recorded status = %s", j.Status)
			}
		}
	}
	if n != 1 {
		t.Fatalf("Record left %d rows for one delivery", n)
	}
	if limited, _ := s.List(ctx, 1); len(limited) != 1 {
		t.Fatalf("List limit ignored: %d", len(limited))
	}
}

func containsID(jobs []Job, id string) bool {
	for _, j := range jobs {
		if j.ID == id {
			return true
		}
	}
	return false
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }
