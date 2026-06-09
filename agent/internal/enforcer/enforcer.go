// Package enforcer is the Agent's per-ticket access mechanism (docs/02 §2.3).
//
// draft10: the default, cross-platform enforcer is a userspace per-ticket L4
// forwarder (package forward) — required because Agents run on Windows where
// nftables does not exist. The Linux kernel nftables engine (package nft) is an
// optional implementation behind this same interface.
package enforcer

import (
	"context"
	"time"
)

// Enforcer programs and revokes per-ticket access. Implementations are the
// single "who can reach what" fact for their Agent.
type Enforcer interface {
	// Setup prepares the enforcer once at startup (no-op for the forwarder;
	// installs nft tables for the kernel engine).
	Setup(ctx context.Context) error

	// Apply ensures access for ruleID: connections from any of srcIPs to
	// dstIP:dstPort, auto-expiring after ttl. It is idempotent (re-Apply of a
	// live ruleID is a no-op returning the same address).
	//
	// forwardAddr is the address the Server must dial to reach the asset:
	//   - userspace forwarder: the Agent's overlay listen address (host:port);
	//   - kernel nft engine: "" (Server dials the asset IP directly, routed
	//     through the Agent).
	Apply(ctx context.Context, ruleID string, srcIPs []string, dstIP string, dstPort uint32, ttl time.Duration) (forwardAddr string, err error)

	// Revoke removes ruleID and drops any active connections (idempotent).
	Revoke(ctx context.Context, ruleID string) error

	// ActiveRuleIDs lists the rule_ids currently enforced (for reconcile).
	ActiveRuleIDs() []string
}
