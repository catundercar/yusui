// Package audit defines the append-only audit sink boundary.
//
// Invariant (CLAUDE.md #7): every state-changing action is audited, including
// system-triggered ones. The audit row must be written in the same DB
// transaction as the action it records (wired in M1 via the Policy Engine).
//
// M0 ships only the interface plus a slog-backed sink. The Postgres sink and
// the §1.8 fail-closed WAL land in M1/M3.
package audit

import (
	"context"
	"log/slog"
)

// Event is a single audit record. Payload must already be redacted of secrets
// (docs/07 §7.7) before reaching a Sink.
type Event struct {
	ActorType  string         // user | system | agent | cron
	ActorID    string         // ulid / username / agent id
	Action     string         // e.g. "ticket.approve", "policy.apply"
	TargetType string         // e.g. "ticket", "agent"
	TargetID   string         // pub_id of the target
	Payload    map[string]any // redacted, optional
}

// Sink persists audit events. Implementations must be append-only.
type Sink interface {
	Write(ctx context.Context, ev Event) error
}

// SlogSink writes audit events to a structured logger. Used in M0 before the
// Postgres-backed sink exists. It never drops silently: a logging sink is an
// honest placeholder, unlike a no-op that would violate the audit invariant.
type SlogSink struct {
	Logger *slog.Logger
}

// Write logs the event at info level.
func (s SlogSink) Write(ctx context.Context, ev Event) error {
	s.Logger.InfoContext(ctx, "audit",
		"action", ev.Action,
		"actor_type", ev.ActorType,
		"actor_id", ev.ActorID,
		"target_type", ev.TargetType,
		"target_id", ev.TargetID,
	)
	return nil
}
