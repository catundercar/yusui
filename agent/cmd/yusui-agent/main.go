// Command yusui-agent is the per-project routing peer / local ACL executor.
//
// M0: build placeholder only. The real control-plane (gRPC over mTLS),
// nftables rule engine (timeout+comment set + SNAT), BoltDB index cache,
// freeze breaker and reconcile loop land in M3 (see docs/02, docs/03).
package main

import (
	"flag"
	"log/slog"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	cfgPath := flag.String("config", "/etc/yusui/agent.yaml", "path to agent config")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("yusui-agent starting", "version", version, "config", *cfgPath)
	logger.Info("M0 placeholder: rule-engine and control-plane are implemented in M3")
}
