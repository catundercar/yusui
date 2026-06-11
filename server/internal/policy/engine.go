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
	"sync"
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
	// ErrSelfApprove is a specific forbidden case (invariant #8: approver ≠
	// requester). It wraps ErrForbidden so generic handling still maps it to 403,
	// while the handler can detect it for a precise client error code.
	ErrSelfApprove = fmt.Errorf("%w: approver must differ from requester", ErrForbidden)
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
	closer     SessionCloser

	// draft10: ticket_id -> Agent forwarder address reported by ApplyRule. This
	// is ephemeral runtime state (a fresh listen port each Apply), so it lives in
	// memory, not the DB. KEY PRESENCE means "applied in this process": an entry
	// may legitimately be "" (kernel nft enforcer / mock gateway dial the asset
	// directly). After a Server restart the map is empty; the scheduler's
	// RebuildForwards re-Applies active tickets to repopulate it (docs/10).
	fwdMu    sync.Mutex
	forwards map[int64]string
}

// SessionCloser force-closes a ticket's live Web Shell sessions on revoke.
type SessionCloser interface {
	ForceCloseByTicket(ctx context.Context, ticketID int64, reason string) error
}

// NewEngine constructs the Policy Engine. srcPeerIPs is the Server's overlay
// IP(s) used as the ACL source (empty until NetBird lands in M4).
func NewEngine(db *store.DB, gw agentgw.Gateway, logger *slog.Logger, srcPeerIPs []string) *Engine {
	return &Engine{db: db, gw: gw, logger: logger, srcPeerIPs: srcPeerIPs, forwards: map[int64]string{}}
}

// ResolveForward returns the Agent forwarder address for a ticket, or "" if the
// asset should be dialled directly (mock gateway / kernel nft enforcer). Web
// Shell consults this when opening a session (draft10).
func (e *Engine) ResolveForward(ticketID int64) string {
	e.fwdMu.Lock()
	defer e.fwdMu.Unlock()
	return e.forwards[ticketID]
}

// setForward records the forwarder address for a ticket. It stores even "" so
// the key's PRESENCE marks the ticket as applied-this-process (so RebuildForwards
// skips it); only Revoke removes the entry via clearForward.
func (e *Engine) setForward(ticketID int64, addr string) {
	e.fwdMu.Lock()
	defer e.fwdMu.Unlock()
	e.forwards[ticketID] = addr
}

func (e *Engine) clearForward(ticketID int64) {
	e.fwdMu.Lock()
	defer e.fwdMu.Unlock()
	delete(e.forwards, ticketID)
}

// forwardKnown reports whether this process has already applied the ticket (so
// the rebuild pass can skip it).
func (e *Engine) forwardKnown(ticketID int64) bool {
	e.fwdMu.Lock()
	defer e.fwdMu.Unlock()
	_, ok := e.forwards[ticketID]
	return ok
}

// SetSessionCloser wires the Web Shell manager (set after construction to avoid
// an import cycle: webshell would otherwise import policy and vice-versa).
func (e *Engine) SetSessionCloser(c SessionCloser) { e.closer = c }

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
				return ErrSelfApprove
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
				return ErrSelfApprove
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
	targets := buildTargets(assets, t.Ports, t.Protocol)
	rid := ruleID(t)
	expires := time.Now().Add(time.Duration(t.DurationSec) * time.Second)

	fwd, err := e.gw.ApplyRule(ctx, agentgw.ApplyInput{
		RuleID: rid, AgentID: agent.ID, SrcPeerIPs: e.srcPeerIPs, Targets: targets, ExpiresAt: expires,
	})
	if err != nil {
		_, _ = e.transition(ctx, ticketID, "approved", "policy.apply_failed", actor,
			map[string]any{"error": err.Error()},
			func(ctx context.Context, q *store.Queries, t store.YusuiTicket) error {
				return q.SetTicketStatus(ctx, store.SetTicketStatusParams{ID: t.ID, Status: "apply_failed"})
			})
		return fmt.Errorf("apply rule on agent %d: %w", agent.ID, err)
	}
	e.setForward(ticketID, fwd) // draft10: Web Shell dials this (if non-empty) instead of the asset IP

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
	// Close any live Web Shell session BEFORE removing the rule (docs/05 §5.4).
	if e.closer != nil {
		if err := e.closer.ForceCloseByTicket(ctx, t.ID, "ticket "+reason); err != nil {
			e.logger.Error("force-close sessions", "ticket", t.PubID, "err", err)
		}
	}
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
	e.clearForward(ticketID) // draft10: drop the ephemeral forwarder address
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

// RebuildForwards re-issues the Agent rule for every active ticket whose
// forwarder address this process does not yet know, repopulating the in-memory
// map after a Server restart (the map is not persisted). The Agent's ApplyRule
// is idempotent on rule_id — it returns the existing forwarder address without
// disrupting live connections, or re-creates the forwarder if the Agent also
// restarted. Tickets whose Agent is not currently connected fail and are retried
// on the next pass; once resolved they are skipped (docs/10). Returns the count
// resolved this pass.
func (e *Engine) RebuildForwards(ctx context.Context) (int, error) {
	active, err := e.db.ListActiveTickets(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range active {
		if e.forwardKnown(t.ID) {
			continue
		}
		fwd, err := e.reapply(ctx, t)
		if err != nil {
			e.logger.Warn("rebuild forward: re-apply failed, will retry", "ticket", t.PubID, "err", err)
			continue
		}
		e.setForward(t.ID, fwd)
		e.logger.Info("rebuilt forward address after restart", "ticket", t.PubID, "forward_addr", fwd)
		n++
	}
	return n, nil
}

// reapply re-issues an active ticket's existing binding to the Agent and returns
// the forwarder address (idempotent; does not change ticket state or the DB).
func (e *Engine) reapply(ctx context.Context, t store.YusuiTicket) (string, error) {
	b, err := e.db.GetBinding(ctx, t.ID)
	if err != nil {
		return "", fmt.Errorf("no binding for ticket %d: %w", t.ID, err)
	}
	assets, err := e.db.ListAssetsByIDs(ctx, t.FrozenAssetIds)
	if err != nil {
		return "", err
	}
	return e.gw.ApplyRule(ctx, agentgw.ApplyInput{
		RuleID: b.AgentRuleID, AgentID: b.AgentID, SrcPeerIPs: e.srcPeerIPs,
		Targets: buildTargets(assets, t.Ports, t.Protocol), ExpiresAt: t.ExpiresAt.Time,
	})
}

// RunScheduler periodically revokes expired tickets and rebuilds forwarder
// addresses until ctx is cancelled. It runs one pass immediately so a restart
// repopulates forwards (and expires overdue tickets) without waiting a full
// interval.
func (e *Engine) RunScheduler(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	e.schedulerPass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.schedulerPass(ctx)
		}
	}
}

func (e *Engine) schedulerPass(ctx context.Context) {
	if n, err := e.ExpireDue(ctx); err != nil {
		e.logger.Error("scheduler expire", "err", err)
	} else if n > 0 {
		e.logger.Info("scheduler revoked expired tickets", "count", n)
	}
	if n, err := e.RebuildForwards(ctx); err != nil {
		e.logger.Error("scheduler rebuild forwards", "err", err)
	} else if n > 0 {
		e.logger.Info("scheduler rebuilt forward addresses", "count", n)
	}
}

// buildTargets expands frozen assets × ticket ports into Agent forward targets.
func buildTargets(assets []store.YusuiAsset, ports []int32, proto string) []agentgw.Target {
	var targets []agentgw.Target
	for _, a := range assets {
		for _, p := range ports {
			targets = append(targets, agentgw.Target{DstIP: a.IpInternal.String(), DstPort: p, Proto: proto})
		}
	}
	return targets
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
