// Command yusui-agent is the per-project NetBird peer + local per-ticket access
// executor: it connects to the Server's Agent Controller over gRPC and programs
// per-ticket access via an Enforcer (docs/02, docs/03).
//
// draft10: the default Enforcer is the cross-platform userspace L4 forwarder
// (works on Windows); set YUSUI_ENFORCER=nft for the optional Linux kernel engine.
//
// Env: YUSUI_SERVER_GRPC, YUSUI_PROJECT, YUSUI_REGISTER_TOKEN,
//
//	YUSUI_HOSTNAME (default os hostname), YUSUI_ENFORCER (forward|nft),
//	YUSUI_LISTEN_HOST (overlay IP to bind forwarders), YUSUI_EGRESS_IFACE (nft masquerade),
//	YUSUI_OVERLAY (static|netbird), and for netbird: YUSUI_NB_IFACE (default wt0),
//	YUSUI_NB_SETUP_KEY, YUSUI_NB_MGMT_URL.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/catundercar/yusui/agent/internal/control"
	"github.com/catundercar/yusui/agent/internal/enforcer"
	"github.com/catundercar/yusui/agent/internal/forward"
	"github.com/catundercar/yusui/agent/internal/nft"
	"github.com/catundercar/yusui/agent/internal/overlay"
)

var version = "0.1.0-m3"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	hostname, _ := os.Hostname()

	cfg := struct {
		serverGRPC, project, token, host, iface, enforcerKind, listenHost, overlayKind string
		nbIface, nbSetupKey, nbMgmtURL                                                 string
	}{
		serverGRPC:   getenv("YUSUI_SERVER_GRPC", "localhost:9090"),
		project:      os.Getenv("YUSUI_PROJECT"),
		token:        os.Getenv("YUSUI_REGISTER_TOKEN"),
		host:         getenv("YUSUI_HOSTNAME", hostname),
		iface:        os.Getenv("YUSUI_EGRESS_IFACE"),
		enforcerKind: getenv("YUSUI_ENFORCER", "forward"),
		listenHost:   os.Getenv("YUSUI_LISTEN_HOST"),
		overlayKind:  getenv("YUSUI_OVERLAY", "static"),
		nbIface:      os.Getenv("YUSUI_NB_IFACE"),
		nbSetupKey:   os.Getenv("YUSUI_NB_SETUP_KEY"),
		nbMgmtURL:    os.Getenv("YUSUI_NB_MGMT_URL"),
	}
	if cfg.project == "" {
		logger.Error("YUSUI_PROJECT is required")
		os.Exit(1)
	}
	logger.Info("yusui-agent starting", "version", version, "server", cfg.serverGRPC, "project", cfg.project)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ov, err := overlay.New(overlay.Config{
		Kind: cfg.overlayKind, ListenHost: cfg.listenHost,
		Iface: cfg.nbIface, SetupKey: cfg.nbSetupKey, MgmtURL: cfg.nbMgmtURL, Logger: logger,
	})
	if err != nil {
		logger.Error("overlay init failed", "err", err)
		os.Exit(1)
	}
	if err := ov.EnsureUp(ctx); err != nil {
		logger.Error("overlay up failed", "err", err)
		os.Exit(1)
	}
	logger.Info("overlay ready", "kind", cfg.overlayKind, "listen_host", ov.ListenHost(), "status", ov.Status())

	var eng enforcer.Enforcer
	switch cfg.enforcerKind {
	case "nft":
		eng = nft.New(cfg.iface, logger) // optional Linux kernel engine (needs CAP_NET_ADMIN + nft)
	default:
		eng = forward.New(ov.ListenHost(), logger) // draft10 default: cross-platform userspace L4 forwarder
	}
	if err := eng.Setup(ctx); err != nil {
		logger.Error("enforcer setup failed", "enforcer", cfg.enforcerKind, "err", err)
		os.Exit(1)
	}
	logger.Info("enforcer ready", "kind", cfg.enforcerKind)

	cli := control.New(cfg.serverGRPC, cfg.project, cfg.token, cfg.host, version, eng, ov, logger)
	if err := cli.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("agent exited", "err", err)
		os.Exit(1)
	}
	logger.Info("yusui-agent stopped")
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
