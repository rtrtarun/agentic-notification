//go:build integration

package budget

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisBudgetAgainstARealRedis runs the shared contract through the real Lua
// script. It needs TEST_REDIS_URL (CI provides one) and is skipped without it.
func TestRedisBudgetAgainstARealRedis(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	contract(t, func(limit int, window time.Duration, clk *clock) Budget {
		return NewRedisBudget(NewRedisStore(client), limit, window).WithClock(clk.now)
	})
}
