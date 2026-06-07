// Package nft is the Agent's nftables rule engine (docs/02 §2.3). It maintains
// table `inet yusui` with a default-drop forward chain and a timeout+comment
// set; per-ticket rules become set elements tagged with the YuSui rule_id.
//
// v0.1 MVP shells out to the `nft` binary (matches the docs syntax). The
// rule_id->elements map is in memory (BoltDB persistence is a later hardening);
// nft is the source of truth, the set timeout auto-expires elements.
package nft

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Engine programs nftables.
type Engine struct {
	iface  string // egress interface for masquerade (optional)
	logger *slog.Logger
	mu     sync.Mutex
	rules  map[string][]string // rule_id -> element keys ("src . dst . port")
}

// New constructs the engine. egressIface enables SNAT masquerade if set.
func New(egressIface string, logger *slog.Logger) *Engine {
	return &Engine{iface: egressIface, logger: logger, rules: map[string][]string{}}
}

func run(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "nft", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("nft %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Setup installs the table/set/chains fresh (drops any prior yusui tables).
func (e *Engine) Setup(ctx context.Context) error {
	_, _ = run(ctx, "", "delete", "table", "inet", "yusui")
	_, _ = run(ctx, "", "delete", "table", "inet", "yusui-nat")

	ruleset := `table inet yusui {
  set allowed_v4 {
    type ipv4_addr . ipv4_addr . inet_service
    flags interval, timeout
  }
  chain forward {
    type filter hook forward priority 0; policy drop;
    ct state established,related accept
    ip saddr . ip daddr . tcp dport @allowed_v4 accept
    log prefix "yusui-drop: " drop
  }
}`
	if _, err := run(ctx, ruleset, "-f", "-"); err != nil {
		return err
	}
	if e.iface != "" {
		nat := fmt.Sprintf(`table inet yusui-nat {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    oifname "%s" masquerade
  }
}`, e.iface)
		if _, err := run(ctx, nat, "-f", "-"); err != nil {
			return err
		}
	}
	e.logger.Info("nftables initialized", "table", "inet yusui", "masquerade_iface", e.iface)
	return nil
}

// Apply adds one set element (src . dst . port) tagged with ruleID, with TTL.
func (e *Engine) Apply(ctx context.Context, ruleID, src, dst string, port uint32, ttl time.Duration) error {
	if src == "" || dst == "" || port == 0 {
		return fmt.Errorf("apply: empty src/dst/port")
	}
	key := fmt.Sprintf("%s . %s . %d", src, dst, port)
	secs := int(ttl.Seconds())
	if secs < 1 {
		secs = 1
	}
	elem := fmt.Sprintf("{ %s timeout %ds comment \"%s\" }", key, secs, ruleID)
	if _, err := run(ctx, "", "add", "element", "inet", "yusui", "allowed_v4", elem); err != nil {
		return err
	}
	e.mu.Lock()
	e.rules[ruleID] = append(e.rules[ruleID], key)
	e.mu.Unlock()
	return nil
}

// Revoke removes all elements for ruleID (idempotent).
func (e *Engine) Revoke(ctx context.Context, ruleID string) error {
	e.mu.Lock()
	keys := e.rules[ruleID]
	delete(e.rules, ruleID)
	e.mu.Unlock()
	for _, key := range keys {
		_, _ = run(ctx, "", "delete", "element", "inet", "yusui", "allowed_v4", "{ "+key+" }")
	}
	return nil
}

// ActiveRuleIDs lists rule_ids currently programmed.
func (e *Engine) ActiveRuleIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.rules))
	for id := range e.rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
