// Command yusui-agent is the per-project routing peer / local ACL executor:
// it connects to the Server's Agent Controller over gRPC and programs nftables
// per-ticket rules (docs/02, docs/03).
//
// Env: YUSUI_SERVER_GRPC, YUSUI_PROJECT, YUSUI_REGISTER_TOKEN,
//
//	YUSUI_HOSTNAME (default os hostname), YUSUI_EGRESS_IFACE (masquerade).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/catundercar/yusui/agent/internal/control"
	"github.com/catundercar/yusui/agent/internal/nft"
)

var version = "0.1.0-m3"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	hostname, _ := os.Hostname()

	cfg := struct {
		serverGRPC, project, token, host, iface string
	}{
		serverGRPC: getenv("YUSUI_SERVER_GRPC", "localhost:9090"),
		project:    os.Getenv("YUSUI_PROJECT"),
		token:      os.Getenv("YUSUI_REGISTER_TOKEN"),
		host:       getenv("YUSUI_HOSTNAME", hostname),
		iface:      os.Getenv("YUSUI_EGRESS_IFACE"),
	}
	if cfg.project == "" {
		logger.Error("YUSUI_PROJECT is required")
		os.Exit(1)
	}
	logger.Info("yusui-agent starting", "version", version, "server", cfg.serverGRPC, "project", cfg.project)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	eng := nft.New(cfg.iface, logger)
	if err := eng.Setup(ctx); err != nil {
		logger.Error("nftables setup failed (need CAP_NET_ADMIN + nft)", "err", err)
		os.Exit(1)
	}

	cli := control.New(cfg.serverGRPC, cfg.project, cfg.token, cfg.host, version, eng, logger)
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
