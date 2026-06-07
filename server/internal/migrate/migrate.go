// Package migrate applies the embedded goose migrations.
//
// Migrations run as the yusui_migrate (DDL owner) role; the serving process
// connects as yusui_app (least privilege). Keep them as separate invocations
// (the `migrate` vs `serve` subcommands) so the long-running server never
// holds DDL credentials.
package migrate

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Up applies all pending migrations using a *sql.DB derived from the pool.
func Up(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
