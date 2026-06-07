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

	"github.com/catundercar/yusui/server/internal/agentgw"
	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/catundercar/yusui/server/internal/config"
	"github.com/catundercar/yusui/server/internal/httpapi"
	"github.com/catundercar/yusui/server/internal/migrate"
	"github.com/catundercar/yusui/server/internal/policy"
	"github.com/catundercar/yusui/server/internal/secrets"
	"github.com/catundercar/yusui/server/internal/services"
	"github.com/catundercar/yusui/server/internal/store"
	"github.com/catundercar/yusui/server/internal/webshell"
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
	if err := cfg.RequireServe(); err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := seedAdmin(ctx, db, cfg, logger); err != nil {
		return err
	}

	mgr := auth.NewManager(cfg.JWTSecret, cfg.AccessTTL, cfg.RefreshTTL)
	idp := auth.NewLocalProvider(db.Queries)
	authH := httpapi.NewAuthHandler(idp, mgr, db.Queries)

	sealer, err := newSealer(cfg, logger)
	if err != nil {
		return err
	}
	catalog := services.NewCatalog(db.Queries, sealer)
	catalogH := httpapi.NewCatalogHandler(catalog, logger)

	gw := agentgw.NewMemory(logger)
	engine := policy.NewEngine(db, gw, logger, cfg.ServerPeerIPs)
	ticketH := httpapi.NewTicketHandler(engine)

	shellMgr := webshell.NewManager(db, catalog, cfg.RecordingsDir, logger)
	engine.SetSessionCloser(shellMgr)
	webShellH := httpapi.NewWebShellHandler(shellMgr, engine, mgr, logger)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Ready: db, Logger: logger, Auth: authH, Catalog: catalogH,
			Ticket: ticketH, WebShell: webShellH, Manager: mgr, StepUpWindow: cfg.StepUpWindow,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go engine.RunScheduler(ctx, 5*time.Second)

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

// seedAdmin creates the first admin account on a fresh DB (dev convenience).
// No-op unless the users table is empty and ADMIN_PASSWORD is set.
func seedAdmin(ctx context.Context, db *store.DB, cfg config.Config, logger *slog.Logger) error {
	if cfg.AdminPassword == "" {
		return nil
	}
	n, err := db.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("seed admin: count users: %w", err)
	}
	if n > 0 {
		return nil
	}
	if err := auth.CheckPolicy(cfg.AdminPassword); err != nil {
		return fmt.Errorf("seed admin: weak ADMIN_PASSWORD: %w", err)
	}
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	displayName := "Administrator"
	if _, err := db.CreateUser(ctx, store.CreateUserParams{
		Username:     cfg.AdminUsername,
		DisplayName:  &displayName,
		Role:         "admin",
		PasswordHash: &hash,
		MfaEnabled:   false,
	}); err != nil {
		return fmt.Errorf("seed admin: create user: %w", err)
	}
	logger.Info("seeded admin user", "username", cfg.AdminUsername)
	return nil
}

// newSealer builds the credential sealer; falls back to JWT_SECRET-derived key
// (dev only) when CREDENTIAL_KEY is unset.
func newSealer(cfg config.Config, logger *slog.Logger) (*secrets.Sealer, error) {
	material := cfg.CredentialKey
	if material == "" {
		logger.Warn("CREDENTIAL_KEY unset; deriving credential key from JWT_SECRET (dev only)")
		material = cfg.JWTSecret
	}
	key, err := secrets.KeyFromString(material)
	if err != nil {
		return nil, err
	}
	return secrets.NewSealer(key, "local-v1")
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
