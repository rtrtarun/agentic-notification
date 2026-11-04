package db

import (
	"context"
	"testing"
	"time"
)

func TestConnectFailsFastWhenDatabaseIsUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Nothing listens on port 1.
	if _, err := Connect(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2", PoolConfig{MaxOpenConns: 5}); err == nil {
		t.Fatal("expected a connection error")
	}
}

func TestConnectRejectsMalformedURL(t *testing.T) {
	if _, err := Connect(context.Background(), "postgres://%zz", PoolConfig{}); err == nil {
		t.Fatal("expected a parse error")
	}
}
