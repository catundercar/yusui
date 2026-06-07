// Package agentgw is the boundary between the Policy Engine and project Agents.
// M1 ships an in-memory mock so the whole control loop runs and is testable
// without a real Agent; M3 swaps in the gRPC/mTLS implementation behind Gateway.
package agentgw

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Target is one allowed asset endpoint for a ticket.
type Target struct {
	DstIP   string `json:"dst_ip"`
	DstPort int32  `json:"dst_port"`
	Proto   string `json:"proto"`
}

// ApplyInput is a per-ticket ACL rule (one rule_id, possibly many targets).
type ApplyInput struct {
	RuleID     string
	AgentID    int64
	SrcPeerIPs []string
	Targets    []Target
	ExpiresAt  time.Time
}

// Gateway pushes/removes per-ticket ACL rules on a project Agent.
type Gateway interface {
	ApplyRule(ctx context.Context, in ApplyInput) error
	RevokeRule(ctx context.Context, agentID int64, ruleID string) error
	Reconcile(ctx context.Context, agentID int64) ([]string, error)
}

// Memory is an in-process mock Gateway recording applied rules.
type Memory struct {
	mu     sync.Mutex
	rules  map[string]ApplyInput
	logger *slog.Logger
}

// NewMemory constructs the mock gateway.
func NewMemory(logger *slog.Logger) *Memory {
	return &Memory{rules: make(map[string]ApplyInput), logger: logger}
}

// ApplyRule records the rule.
func (m *Memory) ApplyRule(_ context.Context, in ApplyInput) error {
	m.mu.Lock()
	m.rules[in.RuleID] = in
	n := len(m.rules)
	m.mu.Unlock()
	m.logger.Info("mock-agent apply", "rule_id", in.RuleID, "agent_id", in.AgentID,
		"targets", len(in.Targets), "expires_at", in.ExpiresAt, "active_rules", n)
	return nil
}

// RevokeRule removes the rule (idempotent).
func (m *Memory) RevokeRule(_ context.Context, agentID int64, ruleID string) error {
	m.mu.Lock()
	delete(m.rules, ruleID)
	n := len(m.rules)
	m.mu.Unlock()
	m.logger.Info("mock-agent revoke", "rule_id", ruleID, "agent_id", agentID, "active_rules", n)
	return nil
}

// Reconcile returns the rule ids currently active on the given agent.
func (m *Memory) Reconcile(_ context.Context, agentID int64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, in := range m.rules {
		if in.AgentID == agentID {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Has reports whether a rule is currently applied (inspection/tests).
func (m *Memory) Has(ruleID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rules[ruleID]
	return ok
}
