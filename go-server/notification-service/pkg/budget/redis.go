package budget

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisKeyPrefix = "attention:budget:"

// Store is the slice of Redis the budget needs, kept small so it can be faked.
type Store interface {
	// Slide atomically drops entries older than the window, and if fewer than limit remain
	// adds one under member. It reports whether it added one, how many entries the window
	// then holds, and the time (milliseconds since the epoch) of the oldest entry when full.
	Slide(ctx context.Context, key string, nowMs, windowMs int64, limit int, member string) (allowed bool, used int, oldestMs int64, err error)
}

// RedisBudget shares one rolling window across replicas through a Store.
type RedisBudget struct {
	store  Store
	limit  int
	window time.Duration
	now    func() time.Time
}

// NewRedisBudget returns a budget of limit interruptions per window. Non-positive values use the defaults.
func NewRedisBudget(store Store, limit int, window time.Duration) *RedisBudget {
	if limit <= 0 {
		limit = DefaultPerHour
	}
	if window <= 0 {
		window = DefaultWindow
	}
	return &RedisBudget{store: store, limit: limit, window: window, now: time.Now}
}

// WithClock replaces the clock; tests use it.
func (b *RedisBudget) WithClock(now func() time.Time) *RedisBudget {
	b.now = now
	return b
}

// Take implements Budget.
func (b *RedisBudget) Take(ctx context.Context, scope string) (Result, error) {
	member, err := newMember()
	if err != nil {
		return Result{}, err
	}
	now := b.now()
	allowed, used, oldestMs, err := b.store.Slide(ctx, redisKeyPrefix+scope, now.UnixMilli(), b.window.Milliseconds(), b.limit, member)
	if err != nil {
		return Result{}, fmt.Errorf("attention budget: %w", err)
	}
	res := Result{Allowed: allowed, Used: used, Limit: b.limit}
	if !allowed {
		res.ResetAt = time.UnixMilli(oldestMs).Add(b.window)
	}
	return res, nil
}

func newMember() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("budget member: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// slideScript is the whole decision in one round trip, so replicas cannot both take the last slot.
var slideScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', tonumber(ARGV[1]) - tonumber(ARGV[2]))
local n = redis.call('ZCARD', KEYS[1])
if n < tonumber(ARGV[3]) then
  redis.call('ZADD', KEYS[1], ARGV[1], ARGV[4])
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return {1, n + 1, 0}
end
local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
return {0, n, tonumber(oldest[2])}
`)

// RedisStore adapts a go-redis client to Store.
type RedisStore struct{ client redis.Scripter }

// NewRedisStore wraps a go-redis client.
func NewRedisStore(client redis.Scripter) *RedisStore { return &RedisStore{client: client} }

// Slide implements Store.
func (s *RedisStore) Slide(ctx context.Context, key string, nowMs, windowMs int64, limit int, member string) (bool, int, int64, error) {
	raw, err := slideScript.Run(ctx, s.client, []string{key}, nowMs, windowMs, limit, member).Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(raw) != 3 {
		return false, 0, 0, errors.New("unexpected reply from the budget script")
	}
	vals := make([]int64, 3)
	for i, v := range raw {
		n, ok := v.(int64)
		if !ok {
			return false, 0, 0, fmt.Errorf("unexpected reply type %T from the budget script", v)
		}
		vals[i] = n
	}
	return vals[0] == 1, int(vals[1]), vals[2], nil
}
