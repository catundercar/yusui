// Package policy is the Policy Engine: the sole owner of the ticket state
// machine. Every transition writes its audit row in the SAME DB transaction as
// the status change (invariant #7). External Agent calls happen OUTSIDE the tx
// (docs/05 §5.6.2). v0.1 is single-layer: only the Agent gateway is touched.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/catundercar/yusui/server/internal/agentgw"
	"github.com/catundercar/yusui/server/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oklog/ulid/v2"
)

// Sentinel errors mapped to HTTP status by handlers.
var (
	ErrNotFound   = errors.New("ticket not found")
	ErrConflict   = errors.New("ticket not in expected state")
	ErrForbidden  = errors.New("forbidden")
	ErrValidation = errors.New("validation")
)

// Actor identifies the trigger of a transition, for audit.
type Actor struct {
	Type string // user | system | cron | agent
	ID   string
}

// SystemActor is used by the scheduler/reconciler.
func SystemActor(id string) Actor { return Actor{Type: "system", ID: id} }

// Engine owns ticket transitions.
type Engine struct {
	db         *store.DB
	gw         agentgw.Gateway
	logger     *slog.Logger
	srcPeerIPs []string
}

// NewEngine constructs the Policy Engine. srcPeerIPs is the Server's overlay
// IP(s) used as the ACL source (empty until NetBird lands in M4).
func NewEngine(db *store.DB, gw agentgw.Gateway, logger *slog.Logger, srcPeerIPs []string) *Engine {
	return &Engine{db: db, gw: gw, logger: logger, srcPeerIPs: srcPeerIPs}
}

// ---- reads ----

func (e *Engine) Get(ctx context.Context, id int64) (store.YusuiTicket, error) {
	return e.db.GetTicketByID(ctx, id)
}
func (e *Engine) List(ctx context.Context) ([]store.YusuiTicket, error) {
	return e.db.ListTickets(ctx)
}
func (e *Engine) ListByRequester(ctx context.Context, requesterID int64) ([]store.YusuiTicket, error) {
	return e.db.ListTicketsByRequester(ctx, requesterID)
}

// ---- submit ----

// SubmitInput is a new-ticket request (target_selector is asset_ids in v0.1).
type SubmitInput struct {
	RequesterID int64
	ProjectID   int64
	AssetIDs    []int64
	Ports       []int32
	Protocol    string
	AccessKind  string
	Reason      string
	DurationSec int32
}

// Submit creates a pending ticket (+audit in one tx).
func (e *Engine) Submit(ctx context.Context, in SubmitInput, actor Actor) (store.YusuiTicket, error) {
	if in.Reason == "" {
		return store.YusuiTicket{}, fmt.Errorf("%w: reason is required", ErrValidation)
	}
	if in.DurationSec < 60 || in.DurationSec > 86400 {
		return store.YusuiTicket{}, fmt.Errorf("%w: duration_sec must be 60..86400", ErrValidation)
	}
	if len(in.AssetIDs) == 0 {
		return store.YusuiTicket{}, fmt.Errorf("%w: asset_ids is required", ErrValidation)
	}
	if len(in.Ports) == 0 {
		return store.YusuiTicket{}, fmt.Errorf("%w: ports is required", ErrValidation)
	}
	if in.Protocol == "" {
		in.Protocol = "tcp"
	}
	if in.AccessKind == "" {
		in.AccessKind = "web_shell"
	}
	if _, err := e.db.GetProjectByID(ctx, in.ProjectID); err != nil {
		return store.YusuiTicket{}, fmt.Errorf("%w: project %d not found", ErrValidation, in.ProjectID)
	}
	sel, _ := json.Marshal(map[string]any{"asset_ids": in.AssetIDs})

	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		return store.YusuiTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := e.db.WithTx(tx)
	t, err := q.CreateTicket(ctx, store.CreateTicketParams{
		PubID: ulid.Make().String(), RequesterID: in.RequesterID, ProjectID: in.ProjectID,
		TargetSelector: sel, Ports: in.Ports, Protocol: in.Protocol,
		AccessKind: in.AccessKind, Reason: in.Reason, DurationSec: in.DurationSec,
	})
	if err != nil {
		return store.YusuiTicket{}, err
	}
	if err := audit(ctx, q, actor, "ticket.submit", t.PubID, map[string]any{"project_id": in.ProjectID, "assets": in.AssetIDs}); err != nil {
		return store.YusuiTicket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.YusuiTicket{}, err
	}
	return t, nil
}

// ---- transitions ----

// Approve moves pending→approved (freezing the asset set) then Apply→active.
func (e *Engine) Approve(ctx context.Context, ticketID, approverID int64, actor Actor) (store.YusuiTicket, error) {
	_, err := e.transition(ctx, ticketID, "pending", "ticket.approve", actor, nil,
		func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
			if t.RequesterID == approverID {
				return fmt.Errorf("%w: approver must differ from requester", ErrForbidden)
			}
			assetIDs, err := parseAssetIDs(t.TargetSelector)
			if err != nil {
				return err
			}
			assets, err := q.ListAssetsByIDs(ctx, assetIDs)
			if err != nil {
				return err
			}
			if len(assets) == 0 {
				return fmt.Errorf("%w: no assets match target_selector", ErrValidation)
			}
			for _, a := range assets {
				if a.ProjectID != t.ProjectID {
					return fmt.Errorf("%w: asset %d not in project %d", ErrValidation, a.ID, t.ProjectID)
				}
			}
			aid := approverID
			return q.SetTicketApproved(ctx, store.SetTicketApprovedParams{ID: t.ID, ApproverID: &aid, FrozenAssetIds: assetIDs})
		})
	if err != nil {
		return store.YusuiTicket{}, err
	}
	if err := e.Apply(ctx, ticketID, actor); err != nil {
		return store.YusuiTicket{}, err
	}
	return e.db.GetTicketByID(ctx, ticketID)
}

// Reject moves pending→rejected.
func (e *Engine) Reject(ctx context.Context, ticketID, approverID int64, reason string, actor Actor) (store.YusuiTicket, error) {
	return e.transition(ctx, ticketID, "pending", "ticket.reject", actor, map[string]any{"reason": reason},
		func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
			if t.RequesterID == approverID {
				return fmt.Errorf("%w: approver must differ from requester", ErrForbidden)
			}
			return q.CloseTicket(ctx, store.CloseTicketParams{ID: t.ID, Status: "rejected"})
		})
}

// Apply pushes the Agent rule then moves approved→active. The gateway call is
// outside the tx; on failure the ticket goes to apply_failed.
func (e *Engine) Apply(ctx context.Context, ticketID int64, actor Actor) error {
	t, err := e.db.GetTicketByID(ctx, ticketID)
	if err != nil {
		return ErrNotFound
	}
	if t.Status != "approved" {
		return fmt.Errorf("%w: apply requires approved (have %q)", ErrConflict, t.Status)
	}
	agent, err := e.db.GetPrimaryAgentForProject(ctx, t.ProjectID)
	if err != nil {
		return fmt.Errorf("%w: no primary agent for project %d", ErrValidation, t.ProjectID)
	}
	assets, err := e.db.ListAssetsByIDs(ctx, t.FrozenAssetIds)
	if err != nil {
		return err
	}
	var targets []agentgw.Target
	for _, a := range assets {
		for _, p := range t.Ports {
			targets = append(targets, agentgw.Target{DstIP: a.IpInternal.String(), DstPort: p, Proto: t.Protocol})
		}
	}
	rid := ruleID(t)
	expires := time.Now().Add(time.Duration(t.DurationSec) * time.Second)

	if err := e.gw.ApplyRule(ctx, agentgw.ApplyInput{
		RuleID: rid, AgentID: agent.ID, SrcPeerIPs: e.srcPeerIPs, Targets: targets, ExpiresAt: expires,
	}); err != nil {
		_, _ = e.transition(ctx, ticketID, "approved", "policy.apply_failed", actor,
			map[string]any{"error": err.Error()},
			func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
				return q.SetTicketStatus(ctx, store.SetTicketStatusParams{ID: t.ID, Status: "apply_failed"})
			})
		return fmt.Errorf("apply rule on agent %d: %w", agent.ID, err)
	}

	_, err = e.transition(ctx, ticketID, "approved", "policy.apply", actor,
		map[string]any{"agent_id": agent.ID, "rule_id": rid, "targets": len(targets), "expires_at": expires},
		func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
			if err := q.UpsertBinding(ctx, store.UpsertBindingParams{
				TicketID: t.ID, AgentID: agent.ID, AgentRuleID: rid, SrcPeerIps: toAddrs(e.srcPeerIPs),
			}); err != nil {
				return err
			}
			if err := q.SetBindingApplied(ctx, t.ID); err != nil {
				return err
			}
			return q.SetTicketActive(ctx, store.SetTicketActiveParams{ID: t.ID, ExpiresAt: pgTime(expires)})
		})
	return err
}

// Revoke moves active→revoking→closed, removing the Agent rule in between.
func (e *Engine) Revoke(ctx context.Context, ticketID int64, reason string, actor Actor) error {
	t, err := e.transition(ctx, ticketID, "active", "ticket.revoke", actor, map[string]any{"reason": reason},
		func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
			return q.SetTicketStatus(ctx, store.SetTicketStatusParams{ID: t.ID, Status: "revoking"})
		})
	if err != nil {
		return err
	}
	// M2: SessionSvc.ForceCloseByTicket(ticketID) goes here (before rule revoke).
	if b, err := e.db.GetBinding(ctx, t.ID); err == nil {
		if err := e.gw.RevokeRule(ctx, b.AgentID, b.AgentRuleID); err != nil {
			e.logger.Error("revoke rule failed", "ticket", t.PubID, "err", err)
			_, _ = e.transition(ctx, ticketID, "revoking", "policy.revoke_pending", actor, nil,
				func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
					return q.SetTicketStatus(ctx, store.SetTicketStatusParams{ID: t.ID, Status: "revoke_pending"})
				})
			return fmt.Errorf("revoke rule: %w", err)
		}
	}
	_, err = e.transition(ctx, ticketID, "revoking", "policy.revoke", actor, map[string]any{"reason": reason},
		func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
			return q.CloseTicket(ctx, store.CloseTicketParams{ID: t.ID, Status: "closed"})
		})
	return err
}

// ExpireDue revokes all active tickets past their expiry (scheduler entrypoint).
func (e *Engine) ExpireDue(ctx context.Context) (int, error) {
	due, err := e.db.ListExpiredActiveTickets(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range due {
		if err := e.Revoke(ctx, t.ID, "expired", SystemActor("scheduler")); err != nil {
			e.logger.Error("expire revoke failed", "ticket", t.PubID, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// RunScheduler periodically revokes expired tickets until ctx is cancelled.
func (e *Engine) RunScheduler(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := e.ExpireDue(ctx); err != nil {
				e.logger.Error("scheduler expire", "err", err)
			} else if n > 0 {
				e.logger.Info("scheduler revoked expired tickets", "count", n)
			}
		}
	}
}

// ---- internals ----

// transition runs a guarded status change + audit in one tx (invariant #7).
func (e *Engine) transition(
	ctx context.Context, ticketID int64, from, action string, actor Actor, payload any,
	mutate func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error,
) (store.YusuiTicket, error) {
	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		return store.YusuiTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := e.db.WithTx(tx)

	t, err := q.GetTicketForUpdate(ctx, ticketID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.YusuiTicket{}, ErrNotFound
		}
		return store.YusuiTicket{}, err
	}
	if t.Status != from {
		return store.YusuiTicket{}, fmt.Errorf("%w: have %q want %q", ErrConflict, t.Status, from)
	}
	if mutate != nil {
		if err := mutate(ctx, q, t); err != nil {
			return store.YusuiTicket{}, err
		}
	}
	if err := audit(ctx, q, actor, action, t.PubID, payload); err != nil {
		return store.YusuiTicket{}, err
	}
	updated, err := q.GetTicketByID(ctx, ticketID)
	if err != nil {
		return store.YusuiTicket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.YusuiTicket{}, err
	}
	return updated, nil
}

func audit(ctx context.Context, q *store.Queries, actor Actor, action, targetID string, payload any) error {
	pb := []byte("{}")
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			pb = b
		}
	}
	tt := "ticket"
	aid := actor.ID
	return q.InsertAudit(ctx, store.InsertAuditParams{
		ActorType: actor.Type, ActorID: &aid, Action: action,
		TargetType: &tt, TargetID: &targetID, Payload: pb,
	})
}

func ruleID(t store.YusuiTicket) string { return fmt.Sprintf("yusui:tk:%d", t.ID) }

func parseAssetIDs(sel []byte) ([]int64, error) {
	var s struct {
		AssetIDs []int64 `json:"asset_ids"`
	}
	if err := json.Unmarshal(sel, &s); err != nil {
		return nil, fmt.Errorf("%w: bad target_selector", ErrValidation)
	}
	if len(s.AssetIDs) == 0 {
		return nil, fmt.Errorf("%w: target_selector.asset_ids is empty", ErrValidation)
	}
	return s.AssetIDs, nil
}

func toAddrs(ss []string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
