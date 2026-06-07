// Command yusui-server is the YuSui control plane + proxy.
//
// Two subcommands keep DDL and runtime privileges separate:
//   - "migrate": apply schema migrations (run as the yusui_migrate role)
//   - "serve"  : run the HTTP server (run as the least-privilege yusui_app role)
//
// Both read DATABASE_URL; deploy supplies different credentials per role.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/catundercar/yusui/server/internal/config"
	"github.com/catundercar/yusui/server/internal/httpapi"
	"github.com/catundercar/yusui/server/internal/migrate"
	"github.com/catundercar/yusui/server/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx := context.Background()
	switch cmd {
	case "migrate":
		return runMigrate(ctx, cfg, logger)
	case "serve":
		return runServe(ctx, cfg, logger)
	default:
		return fmt.Errorf("unknown command %q (want: serve|migrate)", cmd)
	}
}

func runMigrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	logger.Info("applying migrations")
	if err := migrate.Up(ctx, db.Pool); err != nil {
		return err
	}
	logger.Info("migrations applied")
	return nil
}

func runServe(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(db, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		logger.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shCtx)
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
