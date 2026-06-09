// Package control is the Agent's gRPC client: it registers, opens the Control
// stream, and applies/revokes per-ticket access via an Enforcer (docs/03).
package control

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/catundercar/yusui/agent/internal/enforcer"
	agentv1 "github.com/catundercar/yusui/proto/yusui/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client connects an Agent to the Server's Agent Controller.
type Client struct {
	serverAddr    string
	projectCode   string
	registerToken string
	hostname      string
	version       string
	eng           enforcer.Enforcer
	logger        *slog.Logger
}

// New constructs the control client.
func New(serverAddr, projectCode, registerToken, hostname, version string, eng enforcer.Enforcer, logger *slog.Logger) *Client {
	return &Client{serverAddr: serverAddr, projectCode: projectCode, registerToken: registerToken, hostname: hostname, version: version, eng: eng, logger: logger}
}

// Run registers and serves the control stream until ctx is cancelled. It
// reconnects on stream errors (backoff).
func (c *Client) Run(ctx context.Context) error {
	conn, err := grpc.NewClient(c.serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	cli := agentv1.NewAgentControlClient(conn)

	for ctx.Err() == nil {
		if err := c.session(ctx, cli); err != nil && ctx.Err() == nil {
			c.logger.Error("control session ended; reconnecting", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	}
	return ctx.Err()
}

func (c *Client) session(ctx context.Context, cli agentv1.AgentControlClient) error {
	reg, err := cli.Register(ctx, &agentv1.RegisterRequest{
		ProjectCode: c.projectCode, Hostname: c.hostname,
		AgentVersion: c.version, RegisterToken: c.registerToken,
	})
	if err != nil {
		return err
	}
	c.logger.Info("registered", "agent_id", reg.AgentId)

	streamCtx := metadata.AppendToOutgoingContext(ctx, "session-token", reg.SessionToken)
	stream, err := cli.Control(streamCtx)
	if err != nil {
		return err
	}

	var sendMu sync.Mutex
	send := func(m *agentv1.AgentToServer) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(m)
	}

	// heartbeats
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				_ = send(&agentv1.AgentToServer{Msg: &agentv1.AgentToServer_Heartbeat{Heartbeat: &agentv1.Heartbeat{
					Ts: timestamppb.Now(), Status: agentv1.AgentStatus_AGENT_STATUS_READY,
					ActiveRules: uint64(len(c.eng.ActiveRuleIDs())), NetbirdStatus: "n/a",
				}}})
			}
		}
	}()

	c.logger.Info("control stream open")
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch m := msg.Msg.(type) {
		case *agentv1.ServerToAgent_Apply:
			_ = send(c.handleApply(ctx, m.Apply))
		case *agentv1.ServerToAgent_Revoke:
			r := m.Revoke
			res := agentv1.AckResult_ACK_RESULT_OK
			errMsg := ""
			if err := c.eng.Revoke(ctx, r.RuleId); err != nil {
				res = agentv1.AckResult_ACK_RESULT_FAILED
				errMsg = err.Error()
			}
			_ = send(ack(r.CommandId, res, errMsg, ""))
		case *agentv1.ServerToAgent_Reconcile:
			_ = send(&agentv1.AgentToServer{Msg: &agentv1.AgentToServer_ReconcileResp{ReconcileResp: &agentv1.ReconcileResponse{
				CommandId: m.Reconcile.CommandId, ActiveRuleIds: c.eng.ActiveRuleIDs(),
			}}})
		case *agentv1.ServerToAgent_Drain:
			c.logger.Info("drain requested")
		}
	}
}

func (c *Client) handleApply(ctx context.Context, a *agentv1.ApplyRule) *agentv1.AgentToServer {
	ttl := time.Hour
	if a.ExpiresAt != nil {
		ttl = time.Until(a.ExpiresAt.AsTime())
	}
	srcs := a.SrcPeerIps
	if len(srcs) == 0 {
		c.logger.Error("apply failed", "rule", a.RuleId, "err", "no src_peer_ips")
		return ack(a.CommandId, agentv1.AckResult_ACK_RESULT_FAILED, "no src_peer_ips in ApplyRule", "")
	}
	fwd, err := c.eng.Apply(ctx, a.RuleId, srcs, a.DstIp, a.DstPort, ttl)
	if err != nil {
		c.logger.Error("apply failed", "rule", a.RuleId, "err", err)
		return ack(a.CommandId, agentv1.AckResult_ACK_RESULT_FAILED, err.Error(), "")
	}
	// draft10: report the forwarder's overlay listen address; the Server dials it
	// to reach the asset (empty for the kernel nft enforcer).
	c.logger.Info("applied rule", "rule", a.RuleId, "dst", a.DstIp, "port", a.DstPort, "srcs", len(srcs), "forward_addr", fwd)
	return ack(a.CommandId, agentv1.AckResult_ACK_RESULT_OK, "", fwd)
}

func ack(cmdID string, res agentv1.AckResult, errMsg, forwardAddr string) *agentv1.AgentToServer {
	return &agentv1.AgentToServer{Msg: &agentv1.AgentToServer_Ack{Ack: &agentv1.AckCommand{
		CommandId: cmdID, Result: res, ErrorMsg: errMsg, ForwardAddr: forwardAddr,
	}}}
}
