package dedup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/example/notification-service/pkg/event"
)

// fakeStore is an in-memory KeyStore that can be told to fail.
type fakeStore struct {
	mu   sync.Mutex
	data map[string]string
	err  error
}

func newFakeStore() *fakeStore { return &fakeStore{data: map[string]string{}} }

func (f *fakeStore) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if _, ok := f.data[key]; ok {
		return false, nil
	}
	f.data[key] = value
	return true, nil
}

func (f *fakeStore) Get(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeStore) DeleteIfEqual(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.data[key] == value {
		delete(f.data, key)
	}
	return nil
}

func TestRedisDeduperDetectsDuplicateAcrossInstances(t *testing.T) {
	store := newFakeStore()
	replicaA := NewRedisDeduper(store, time.Minute)
	replicaB := NewRedisDeduper(store, time.Minute)

	first := check(replicaA, prEvent("a", 1, "opened"))
	second := check(replicaB, prEvent("b", 1, "opened"))

	if first.Duplicate {
		t.Fatal("first event must not be a duplicate")
	}
	if !second.Duplicate || second.OriginalID != "a" {
		t.Fatalf("replicas must share the window, got %+v", second)
	}
}

func TestRedisDeduperFailsOpenOnStoreError(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New("redis down")
	d := NewRedisDeduper(store, time.Minute)

	res, err := d.Check(context.Background(), prEvent("a", 1, "opened"))
	if err == nil {
		t.Fatal("expected the store error to be reported")
	}
	if res.Duplicate {
		t.Fatal("a store outage must never mark an event as duplicate")
	}
	if res.DedupKey == "" || res.ThreadID == "" {
		t.Fatalf("keys must still be assigned on failure: %+v", res)
	}
}

func TestRedisDeduperForgetAllowsRetry(t *testing.T) {
	d := NewRedisDeduper(newFakeStore(), time.Minute)
	first := check(d, prEvent("a", 1, "opened"))

	if err := d.Forget(context.Background(), first.DedupKey, "a"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if retry := check(d, prEvent("a2", 1, "opened")); retry.Duplicate {
		t.Fatal("after Forget the retry must be treated as new")
	}
}

func TestRedisDeduperForgetOnlyRemovesOwnEntry(t *testing.T) {
	d := NewRedisDeduper(newFakeStore(), time.Minute)
	first := check(d, prEvent("a", 1, "opened"))
	check(d, prEvent("b", 1, "opened")) // duplicate of a

	// b was merged and failed; forgetting b must not erase a's entry.
	if err := d.Forget(context.Background(), first.DedupKey, "b"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if third := check(d, prEvent("c", 1, "opened")); !third.Duplicate || third.OriginalID != "a" {
		t.Fatalf("original entry was removed: %+v", third)
	}
}

func TestRedisDeduperUsesDefaultWindow(t *testing.T) {
	if d := NewRedisDeduper(newFakeStore(), 0); d.window != DefaultWindow {
		t.Fatalf("window = %v", d.window)
	}
}

func TestRedisDeduperGetErrorIsReported(t *testing.T) {
	store := newFakeStore()
	store.data[redisKeyPrefix+DedupKey(prEvent("a", 1, "opened"))] = "a"
	d := NewRedisDeduper(&getFailingStore{fakeStore: store}, time.Minute)
	res, err := d.Check(context.Background(), prEvent("b", 1, "opened"))
	if err == nil || res.Duplicate {
		t.Fatalf("expected reported error and fail-open result, got res=%+v err=%v", res, err)
	}
}

type getFailingStore struct{ *fakeStore }

func (g *getFailingStore) Get(context.Context, string) (string, bool, error) {
	return "", false, errors.New("get failed")
}

var _ KeyStore = (*fakeStore)(nil)
var _ Deduper = (*RedisDeduper)(nil)
var _ Deduper = (*MemoryDeduper)(nil)

func TestPushEventsWithDifferentCommitsAreNotDuplicates(t *testing.T) {
	d := NewMemoryDeduper(time.Minute)
	push := func(id, sha string) event.Event {
		return event.Event{ID: id, Source: "github", EventType: "push", Repository: "octo/repo",
			Payload: map[string]interface{}{"after": sha, "ref": "refs/heads/main"}}
	}
	first := check(d, push("a", "1111"))
	second := check(d, push("b", "2222"))
	redelivered := check(d, push("c", "1111"))

	if first.Duplicate || second.Duplicate {
		t.Fatal("pushes with different head commits are distinct events")
	}
	if !redelivered.Duplicate || redelivered.OriginalID != "a" {
		t.Fatalf("same head commit should be a duplicate, got %+v", redelivered)
	}
}
