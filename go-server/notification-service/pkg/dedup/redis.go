package dedup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/notification-service/pkg/event"
)

const redisKeyPrefix = "dedup:"

// KeyStore is the slice of Redis the deduper needs, kept small so it can be faked.
type KeyStore interface {
	// SetNX stores value under key with a TTL if the key is absent and reports whether it did.
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	// Get returns the value for key; found is false when the key is absent.
	Get(ctx context.Context, key string) (value string, found bool, err error)
	// DeleteIfEqual deletes key only when it still holds value.
	DeleteIfEqual(ctx context.Context, key, value string) error
}

// RedisDeduper shares the dedup window across replicas through a KeyStore.
type RedisDeduper struct {
	store  KeyStore
	window time.Duration
}

// NewRedisDeduper creates a deduper; a non-positive window uses DefaultWindow.
func NewRedisDeduper(store KeyStore, window time.Duration) *RedisDeduper {
	if window <= 0 {
		window = DefaultWindow
	}
	return &RedisDeduper{store: store, window: window}
}

// Check records the event atomically. On a store error it fails open (not a duplicate).
func (d *RedisDeduper) Check(ctx context.Context, evt event.Event) (Result, error) {
	res := Result{DedupKey: DedupKey(evt), ThreadID: ThreadID(evt)}
	key := redisKeyPrefix + res.DedupKey

	set, err := d.store.SetNX(ctx, key, evt.ID, d.window)
	if err != nil {
		return res, fmt.Errorf("dedup set: %w", err)
	}
	if set {
		return res, nil
	}
	original, found, err := d.store.Get(ctx, key)
	if err != nil {
		return res, fmt.Errorf("dedup get: %w", err)
	}
	if found {
		res.Duplicate = true
		res.OriginalID = original
	}
	return res, nil
}

// Forget drops key if it was recorded by event id.
func (d *RedisDeduper) Forget(ctx context.Context, key, id string) error {
	return d.store.DeleteIfEqual(ctx, redisKeyPrefix+key, id)
}

// redisStore adapts a go-redis client to KeyStore.
type redisStore struct {
	client *redis.Client
}

// NewRedisStore wraps client as a KeyStore.
func NewRedisStore(client *redis.Client) KeyStore {
	return &redisStore{client: client}
}

func (s *redisStore) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, value, ttl).Result()
}

func (s *redisStore) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

var deleteIfEqual = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`)

func (s *redisStore) DeleteIfEqual(ctx context.Context, key, value string) error {
	return deleteIfEqual.Run(ctx, s.client, []string{key}, value).Err()
}
