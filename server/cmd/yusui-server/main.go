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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	agentv1 "github.com/catundercar/yusui/proto/yusui/agent/v1"
	"github.com/catundercar/yusui/server/internal/agentctl"
	"github.com/catundercar/yusui/server/internal/agentgw"
	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/catundercar/yusui/server/internal/config"
	"github.com/catundercar/yusui/server/internal/httpapi"
	"github.com/catundercar/yusui/server/internal/migrate"
	"github.com/catundercar/yusui/server/internal/netbird"
	"github.com/catundercar/yusui/server/internal/policy"
	"github.com/catundercar/yusui/server/internal/secrets"
	"github.com/catundercar/yusui/server/internal/services"
	"github.com/catundercar/yusui/server/internal/store"
	"github.com/catundercar/yusui/server/internal/webshell"
	"google.golang.org/grpc"
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
	warnWeakSecrets(cfg, logger)
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
	catalog := services.NewCatalog(db, sealer)
	catalogH := httpapi.NewCatalogHandler(catalog, logger)

	var gw agentgw.Gateway
	var controller *agentctl.Controller
	if cfg.AgentGatewayMode == "grpc" {
		controller = agentctl.New(db, logger, cfg.AgentRegisterToken)
		gw = controller
	} else {
		gw = agentgw.NewMemory(logger)
	}
	engine := policy.NewEngine(db, gw, logger, cfg.ServerPeerIPs)
	if controller != nil {
		controller.SetForwardManager(engine) // rebuild an agent's forwards when it reconnects
	}
	ticketH := httpapi.NewTicketHandler(engine)

	shellMgr := webshell.NewManager(db, catalog, cfg.RecordingsDir, logger)
	engine.SetSessionCloser(shellMgr)
	shellMgr.SetForwardResolver(engine) // draft10: dial the Agent forwarder when present
	webShellH := httpapi.NewWebShellHandler(shellMgr, engine, mgr, logger)

	var grpcSrv *grpc.Server
	if controller != nil {
		lis, lerr := net.Listen("tcp", cfg.AgentGRPCAddr)
		if lerr != nil {
			return fmt.Errorf("agent grpc listen: %w", lerr)
		}
		grpcSrv = grpc.NewServer()
		agentv1.RegisterAgentControlServer(grpcSrv, controller)
		go func() {
			logger.Info("agent gRPC listening", "addr", cfg.AgentGRPCAddr)
			if err := grpcSrv.Serve(lis); err != nil {
				logger.Error("agent grpc serve", "err", err)
			}
		}()
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Ready: db, Logger: logger, Auth: authH, Catalog: catalogH,
			Ticket: ticketH, WebShell: webShellH, Manager: mgr, StepUpWindow: cfg.StepUpWindow,
		}),
		// ReadHeaderTimeout guards against slowloris; IdleTimeout reaps idle
		// keep-alive connections. No Read/WriteTimeout: the Web Shell WebSocket
		// shares this server and would be cut mid-session (the WS library manages
		// its own per-message deadlines after the upgrade).
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	installNetBird(ctx, cfg, logger)

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
		if grpcSrv != nil {
			// GracefulStop blocks until all RPCs finish — but the Agent holds a
			// long-lived Control stream that never ends on its own, so an
			// unbounded GracefulStop would hang every SIGTERM (deploy/restart)
			// holding the listen ports. Bound it: drain briefly, then force.
			done := make(chan struct{})
			go func() { grpcSrv.GracefulStop(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				logger.Warn("gRPC graceful stop timed out; forcing")
				grpcSrv.Stop()
			}
		}
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shCtx)
	}
}

// warnWeakSecrets logs a loud warning (it does NOT fail) when the serve secrets
// look like development placeholders or are too short, so an accidental insecure
// deploy is visible in the logs instead of silent. Production must set strong,
// unique values for JWT_SECRET and CREDENTIAL_KEY.
func warnWeakSecrets(cfg config.Config, logger *slog.Logger) {
	const minLen = 32
	if len(cfg.JWTSecret) < minLen || looksLikeDevSecret(cfg.JWTSecret) {
		logger.Warn("JWT_SECRET looks weak or like a dev default; use a strong random value (>=32 chars) in production", "len", len(cfg.JWTSecret))
	}
	switch {
	case cfg.CredentialKey == "":
		logger.Warn("CREDENTIAL_KEY is unset; the asset-secret sealing key is derived from JWT_SECRET (dev only) — set a dedicated key in production")
	case len(cfg.CredentialKey) < minLen || looksLikeDevSecret(cfg.CredentialKey):
		logger.Warn("CREDENTIAL_KEY looks weak or like a dev default; use a strong value in production", "len", len(cfg.CredentialKey))
	}
}

func looksLikeDevSecret(s string) bool {
	l := strings.ToLower(s)
	for _, m := range []string{"change-me", "changeme", "dev-", "devsecret", "secret", "password", "admin", "test", "example"} {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
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

// installNetBird ensures the single permanent NetBird policy at startup
// (docs/04). Best-effort: failure degrades (blocks new project/agent onboarding)
// but does not stop the server (docs/01 §1.3). No-op unless NETBIRD_ENABLED.
func installNetBird(ctx context.Context, cfg config.Config, logger *slog.Logger) {
	if !cfg.NetBirdEnabled {
		return
	}
	if cfg.NetBirdMgmtURL == "" || cfg.NetBirdToken == "" {
		logger.Warn("NETBIRD_ENABLED but NETBIRD_MGMT_URL/NETBIRD_TOKEN missing; skipping")
		return
	}
	nb := netbird.New(cfg.NetBirdMgmtURL, cfg.NetBirdToken, logger)
	gid, err := nb.EnsureGroup(ctx, "yusui:server-peers")
	if err != nil {
		logger.Error("netbird: ensure server group", "class", netbird.ClassOf(err), "err", err)
		return
	}
	// dst is all project agent groups; populated as projects are created
	// (per-project hook, docs/04 §4.4). At first boot the server group is a
	// placeholder so the policy exists.
	pid, err := nb.EnsureBuiltinPolicy(ctx, "yusui:builtin:server-to-agents", gid, []string{gid})
	if err != nil {
		logger.Error("netbird: ensure builtin policy", "class", netbird.ClassOf(err), "err", err)
		return
	}
	logger.Info("netbird permanent policy ensured", "server_group", gid, "policy", pid)
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
