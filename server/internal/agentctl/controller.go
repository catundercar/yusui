// Package agentctl is the Agent Controller: the gRPC server side of the
// Agent<->Server protocol (docs/03). It also implements agentgw.Gateway, so the
// Policy Engine pushes per-ticket rules over a connected Agent's stream and
// awaits the ack. v0.1 MVP uses a shared register token + session token over the
// (overlay/private) network; mTLS client certs are a documented hardening.
package agentctl

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	agentv1 "github.com/catundercar/yusui/proto/yusui/agent/v1"
	"github.com/catundercar/yusui/server/internal/agentgw"
	"github.com/catundercar/yusui/server/internal/store"
	"github.com/oklog/ulid/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// agentConn is a live Agent control stream.
type agentConn struct {
	agentID int64
	send    chan *agentv1.ServerToAgent
	mu      sync.Mutex
	acks    map[string]chan *agentv1.AckCommand
	recos   map[string]chan *agentv1.ReconcileResponse
}

func (a *agentConn) waitAck(cmdID string) chan *agentv1.AckCommand {
	ch := make(chan *agentv1.AckCommand, 1)
	a.mu.Lock()
	a.acks[cmdID] = ch
	a.mu.Unlock()
	return ch
}
func (a *agentConn) clearAck(cmdID string) { a.mu.Lock(); delete(a.acks, cmdID); a.mu.Unlock() }

// Controller implements agentv1.AgentControlServer and agentgw.Gateway.
type Controller struct {
	agentv1.UnimplementedAgentControlServer
	db       *store.DB
	logger   *slog.Logger
	regToken string
	cmdTO    time.Duration

	mu     sync.Mutex
	conns  map[int64]*agentConn // agentID -> conn
	tokens map[string]int64     // session_token -> agentID
}

// New constructs the controller. regToken is the shared one-time-ish register
// token agents present (empty = accept any, dev only).
func New(db *store.DB, logger *slog.Logger, regToken string) *Controller {
	return &Controller{
		db: db, logger: logger, regToken: regToken, cmdTO: 5 * time.Second,
		conns: map[int64]*agentConn{}, tokens: map[string]int64{},
	}
}

var _ agentgw.Gateway = (*Controller)(nil)

// Register resolves the registering agent to its DB row by (project, hostname)
// and issues a session token. An unknown agent is AUTO-CREATED as a `pending`
// row (docs/11 §11.2): the daemon may connect a heartbeat-only stream, but the
// Policy Engine targets only an approved primary (GetPrimaryAgentForProject), so
// a pending agent receives no per-ticket rules until an admin approves it. Role
// is assigned by the Server, never chosen by the daemon.
func (c *Controller) Register(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	if c.regToken != "" && req.RegisterToken != c.regToken {
		return nil, status.Error(codes.Unauthenticated, "invalid register token")
	}
	if req.Hostname == "" {
		return nil, status.Error(codes.InvalidArgument, "hostname is required")
	}
	proj, err := c.db.GetProjectByCode(ctx, req.ProjectCode)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "project %q not found", req.ProjectCode)
	}
	agent, err := c.db.GetAgentByProjectAndHostname(ctx, store.GetAgentByProjectAndHostnameParams{ProjectID: proj.ID, Hostname: req.Hostname})
	if err != nil {
		role, rerr := c.pickRole(ctx, proj.ID)
		if rerr != nil {
			return nil, rerr
		}
		agent, err = c.db.CreatePendingAgent(ctx, store.CreatePendingAgentParams{ProjectID: proj.ID, Role: role, Hostname: req.Hostname})
		if err != nil {
			// Likely UNIQUE(project_id, role) lost a race with a concurrent
			// auto-register; the daemon retries Register and re-picks a role.
			return nil, status.Errorf(codes.Aborted, "create pending agent: %v", err)
		}
		c.logger.Info("agent auto-registered (pending)", "agent_id", agent.ID, "project", req.ProjectCode, "hostname", req.Hostname, "role", role)
	}
	tok := ulid.Make().String()
	c.mu.Lock()
	c.tokens[tok] = agent.ID
	c.mu.Unlock()
	setupKey := ""
	if agent.Enrollment == "approved" && agent.NetbirdSetupKey != nil {
		setupKey = *agent.NetbirdSetupKey
	}
	c.logger.Info("agent register", "agent_id", agent.ID, "project", req.ProjectCode, "hostname", req.Hostname, "enrollment", agent.Enrollment, "version", req.AgentVersion)
	return &agentv1.RegisterResponse{
		AgentId:         strconv.FormatInt(agent.ID, 10),
		SessionToken:    tok,
		Config:          &agentv1.ControlConfig{HeartbeatSec: 10, FreezeAfterSec: 60, ReconcileIntervalSec: 300},
		Enrollment:      agent.Enrollment,
		NetbirdSetupKey: setupKey,
	}, nil
}

// pickRole assigns the next free agent role for a project: primary, then
// secondary, then exhausted. UNIQUE(project_id, role) is the real guard against
// a race; this is the cooperative path.
func (c *Controller) pickRole(ctx context.Context, projectID int64) (string, error) {
	existing, err := c.db.ListAgentsByProject(ctx, projectID)
	if err != nil {
		return "", status.Errorf(codes.Internal, "list project agents: %v", err)
	}
	taken := map[string]bool{}
	for _, a := range existing {
		taken[a.Role] = true
	}
	switch {
	case !taken["primary"]:
		return "primary", nil
	case !taken["secondary"]:
		return "secondary", nil
	default:
		return "", status.Error(codes.ResourceExhausted, "project agent slots full (primary+secondary)")
	}
}

// Control runs the bidirectional stream for one agent.
func (c *Controller) Control(stream agentv1.AgentControl_ControlServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	tok := ""
	if v := md.Get("session-token"); len(v) > 0 {
		tok = v[0]
	}
	c.mu.Lock()
	agentID, ok := c.tokens[tok]
	c.mu.Unlock()
	if !ok {
		return status.Error(codes.Unauthenticated, "invalid session token")
	}

	conn := &agentConn{
		agentID: agentID,
		send:    make(chan *agentv1.ServerToAgent, 64),
		acks:    map[string]chan *agentv1.AckCommand{},
		recos:   map[string]chan *agentv1.ReconcileResponse{},
	}
	c.mu.Lock()
	c.conns[agentID] = conn
	c.mu.Unlock()
	_ = c.db.SetAgentStatus(stream.Context(), store.SetAgentStatusParams{ID: agentID, Status: "online"})
	c.logger.Info("agent stream connected", "agent_id", agentID)
	defer func() {
		c.mu.Lock()
		delete(c.conns, agentID)
		c.mu.Unlock()
		_ = c.db.SetAgentStatus(context.Background(), store.SetAgentStatusParams{ID: agentID, Status: "offline"})
		c.logger.Info("agent stream disconnected", "agent_id", agentID)
	}()

	// send pump
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		for {
			select {
			case <-stream.Context().Done():
				return
			case msg := <-conn.send:
				if err := stream.Send(msg); err != nil {
					return
				}
			}
		}
	}()

	for {
		in, err := stream.Recv()
		if err != nil {
			return err
		}
		switch m := in.Msg.(type) {
		case *agentv1.AgentToServer_Ack:
			if m.Ack.ForwardAddr != "" {
				// draft10: the Agent's userspace forwarder reported its overlay
				// listen address. Persisting it on the binding + having Web Shell
				// dial it is the next step (needs a real-agent test harness).
				c.logger.Info("agent forward address", "cmd", m.Ack.CommandId, "forward_addr", m.Ack.ForwardAddr)
			}
			conn.mu.Lock()
			if ch := conn.acks[m.Ack.CommandId]; ch != nil {
				ch <- m.Ack
			}
			conn.mu.Unlock()
		case *agentv1.AgentToServer_ReconcileResp:
			conn.mu.Lock()
			if ch := conn.recos[m.ReconcileResp.CommandId]; ch != nil {
				ch <- m.ReconcileResp
			}
			conn.mu.Unlock()
		case *agentv1.AgentToServer_Heartbeat:
			_ = c.db.SetAgentStatus(stream.Context(), store.SetAgentStatusParams{ID: agentID, Status: "online"})
		case *agentv1.AgentToServer_RuleEvent:
			c.logger.Debug("agent rule event", "agent_id", agentID, "rule", m.RuleEvent.RuleId, "kind", m.RuleEvent.Kind)
		}
	}
}

func (c *Controller) connFor(agentID int64) *agentConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conns[agentID]
}

// ---- agentgw.Gateway ----

// ApplyRule sends one ApplyRule per target (carrying all src_peer_ips) sharing
// the rule_id, awaiting each ack. It returns the first target's forwarder
// address (draft10: the Agent opens one userspace listener per target and
// reports its overlay address via AckCommand.forward_addr).
func (c *Controller) ApplyRule(ctx context.Context, in agentgw.ApplyInput) (string, error) {
	conn := c.connFor(in.AgentID)
	if conn == nil {
		return "", fmt.Errorf("agent %d not connected", in.AgentID)
	}
	srcs := in.SrcPeerIPs
	if len(srcs) == 0 {
		srcs = []string{""} // agent falls back to its configured server_peer_set
	}
	var exp *timestamppb.Timestamp
	if !in.ExpiresAt.IsZero() {
		exp = timestamppb.New(in.ExpiresAt)
	}
	forwardAddr := ""
	for _, t := range in.Targets {
		cmdID := ulid.Make().String()
		msg := &agentv1.ServerToAgent{Msg: &agentv1.ServerToAgent_Apply{Apply: &agentv1.ApplyRule{
			CommandId: cmdID, RuleId: in.RuleID, SrcPeerIps: srcs,
			DstIp: t.DstIP, DstPort: uint32(t.DstPort), Proto: protoOf(t.Proto), ExpiresAt: exp,
		}}}
		a, err := c.roundtrip(ctx, conn, cmdID, msg)
		if err != nil {
			return "", err
		}
		if forwardAddr == "" && a.ForwardAddr != "" {
			forwardAddr = a.ForwardAddr // MVP tickets are single-target
		}
	}
	return forwardAddr, nil
}

// RevokeRule removes all elements tagged rule_id.
func (c *Controller) RevokeRule(ctx context.Context, agentID int64, ruleID string) error {
	conn := c.connFor(agentID)
	if conn == nil {
		return fmt.Errorf("agent %d not connected", agentID)
	}
	cmdID := ulid.Make().String()
	msg := &agentv1.ServerToAgent{Msg: &agentv1.ServerToAgent_Revoke{Revoke: &agentv1.RevokeRule{
		CommandId: cmdID, RuleId: ruleID, Reason: "revoked",
	}}}
	_, err := c.roundtrip(ctx, conn, cmdID, msg)
	return err
}

// Reconcile asks the agent for its active rule ids.
func (c *Controller) Reconcile(ctx context.Context, agentID int64) ([]string, error) {
	conn := c.connFor(agentID)
	if conn == nil {
		return nil, fmt.Errorf("agent %d not connected", agentID)
	}
	cmdID := ulid.Make().String()
	ch := make(chan *agentv1.ReconcileResponse, 1)
	conn.mu.Lock()
	conn.recos[cmdID] = ch
	conn.mu.Unlock()
	defer func() { conn.mu.Lock(); delete(conn.recos, cmdID); conn.mu.Unlock() }()
	conn.send <- &agentv1.ServerToAgent{Msg: &agentv1.ServerToAgent_Reconcile{Reconcile: &agentv1.ReconcileRequest{CommandId: cmdID}}}
	select {
	case <-time.After(c.cmdTO):
		return nil, fmt.Errorf("reconcile timeout for agent %d", agentID)
	case r := <-ch:
		return r.ActiveRuleIds, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Controller) roundtrip(ctx context.Context, conn *agentConn, cmdID string, msg *agentv1.ServerToAgent) (*agentv1.AckCommand, error) {
	ack := conn.waitAck(cmdID)
	defer conn.clearAck(cmdID)
	select {
	case conn.send <- msg:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(c.cmdTO):
		return nil, fmt.Errorf("send timeout")
	}
	select {
	case a := <-ack:
		if a.Result != agentv1.AckResult_ACK_RESULT_OK && a.Result != agentv1.AckResult_ACK_RESULT_SKIPPED {
			return nil, fmt.Errorf("agent nacked %s: %s", a.Result, a.ErrorMsg)
		}
		return a, nil
	case <-time.After(c.cmdTO):
		return nil, fmt.Errorf("ack timeout for command %s", cmdID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func protoOf(s string) agentv1.Protocol {
	switch s {
	case "udp":
		return agentv1.Protocol_PROTOCOL_UDP
	case "any":
		return agentv1.Protocol_PROTOCOL_ANY
	default:
		return agentv1.Protocol_PROTOCOL_TCP
	}
}
