// Package store owns the Postgres connection pool and the sqlc-generated
// query layer. It returns concrete structs; callers depend on small,
// consumer-defined interfaces (CLAUDE.md: accept interfaces, return structs).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB bundles the pgx pool with the sqlc-generated Queries.
type DB struct {
	Pool *pgxpool.Pool
	*Queries
}

// Open creates the pool and verifies connectivity, retrying the initial ping
// for up to ~30s so the process tolerates a still-starting Postgres (e.g. on
// first `docker compose up`) without relying on an orchestrator healthcheck.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: new pool: %w", err)
	}
	if err := pingWithRetry(ctx, pool, 30*time.Second); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool, Queries: New(pool)}, nil
}

func pingWithRetry(ctx context.Context, pool *pgxpool.Pool, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	var last error
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return fmt.Errorf("store: ping after retries: %w", last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Close releases the pool.
func (db *DB) Close() { db.Pool.Close() }
