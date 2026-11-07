// Command migrate creates or updates the database schema and exits. The Go service does the same at start-up;
// this command exists so other services that share the database (the Java service, tests) can have the schema
// without running the whole service. Every statement is idempotent, so running it twice is safe.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/example/notification-service/pkg/db"
)

const timeout = 60 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate failed:", err)
		os.Exit(1)
	}
	fmt.Println("schema is up to date")
}

func run() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := db.Connect(ctx, url, db.PoolConfig{})
	if err != nil {
		return err
	}
	defer conn.Close()
	return db.MigrateLocked(ctx, conn)
}
