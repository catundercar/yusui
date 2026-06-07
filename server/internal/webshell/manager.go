package webshell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/catundercar/yusui/server/internal/store"
	"github.com/oklog/ulid/v2"
	"golang.org/x/crypto/ssh"
)

// CredentialOpener decrypts an asset's active credential (services.Catalog).
type CredentialOpener interface {
	OpenCredentialSecret(ctx context.Context, assetID int64) (sshUser, secret, authKind string, err error)
}

// Manager owns live sessions (one per ticket in v0.1) and force-close.
type Manager struct {
	db      *store.DB
	creds   CredentialOpener
	ruleset *Ruleset
	recDir  string
	logger  *slog.Logger
	dialTO  time.Duration

	mu       sync.Mutex
	sessions map[int64]*Session // ticketID -> session
}

// NewManager constructs the Web Shell manager.
func NewManager(db *store.DB, creds CredentialOpener, recDir string, logger *slog.Logger) *Manager {
	return &Manager{
		db: db, creds: creds, ruleset: DefaultRuleset(), recDir: recDir,
		logger: logger, dialTO: 10 * time.Second, sessions: map[int64]*Session{},
	}
}

// Ruleset returns the active command-filter ruleset.
func (m *Manager) Ruleset() *Ruleset { return m.ruleset }

// OpenForTicket returns the existing session for the ticket or dials a new one.
func (m *Manager) OpenForTicket(ctx context.Context, t store.YusuiTicket) (*Session, error) {
	m.mu.Lock()
	if s, ok := m.sessions[t.ID]; ok {
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	if len(t.FrozenAssetIds) == 0 || len(t.Ports) == 0 {
		return nil, errors.New("ticket has no frozen assets/ports")
	}
	assetID := t.FrozenAssetIds[0]
	asset, err := m.db.GetAssetByID(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("asset: %w", err)
	}
	agent, err := m.db.GetPrimaryAgentForProject(ctx, t.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	sshUser, secret, authKind, err := m.creds.OpenCredentialSecret(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("credential: %w", err)
	}

	client, err := dialSSH(asset.IpInternal.String(), t.Ports[0], sshUser, secret, authKind, m.dialTO)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, err
	}

	snap, _ := json.Marshal(map[string]any{"rules": m.ruleset.RuleIDs()})
	row, err := m.db.CreateSession(ctx, store.CreateSessionParams{
		PubID: ulid.Make().String(), TicketID: t.ID, AssetID: assetID,
		AgentID: agent.ID, SshUser: sshUser, CommandPolicySnapshot: snap,
	})
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("create session: %w", err)
	}
	rec, uri, err := NewRecorder(m.recDir, row.PubID, 80, 24, time.Now())
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("recorder: %w", err)
	}

	s := &Session{
		DBID: row.ID, PubID: row.PubID, TicketID: t.ID, Ruleset: m.ruleset, Recorder: rec,
		client: client, sess: sess, stdin: stdin, logger: m.logger,
		subs: map[chan []byte]struct{}{}, closedCh: make(chan struct{}),
	}
	s.onClose = func(reason string) {
		m.mu.Lock()
		delete(m.sessions, t.ID)
		m.mu.Unlock()
		r := reason
		u := uri
		_ = m.db.CloseSession(context.Background(), store.CloseSessionParams{ID: row.ID, ClosedReason: &r, RecordingUri: &u})
	}

	if err := m.db.SetSessionRunning(ctx, row.ID); err != nil {
		m.logger.Error("set session running", "err", err)
	}
	m.mu.Lock()
	m.sessions[t.ID] = s
	m.mu.Unlock()
	go s.pumpStdout(stdout)
	m.logger.Info("session opened", "session", row.PubID, "ticket", t.PubID,
		"asset", asset.IpInternal.String(), "port", t.Ports[0], "ssh_user", sshUser)
	return s, nil
}

// ForceCloseByTicket closes a ticket's live session (Policy Engine on revoke).
func (m *Manager) ForceCloseByTicket(_ context.Context, ticketID int64, reason string) error {
	m.mu.Lock()
	s := m.sessions[ticketID]
	m.mu.Unlock()
	if s != nil {
		s.Close(reason)
	}
	return nil
}

// AddAttacher records an attacher in session history; returns its id (0 on error).
func (m *Manager) AddAttacher(ctx context.Context, sessionDBID int64, userID *int64, source, label, role string) int64 {
	lbl := label
	id, err := m.db.AddAttacher(ctx, store.AddAttacherParams{
		SessionID: sessionDBID, UserID: userID, Source: source, Label: &lbl, Role: role,
	})
	if err != nil {
		m.logger.Error("add attacher", "err", err)
		return 0
	}
	return id
}

// DetachAttacher marks an attacher detached.
func (m *Manager) DetachAttacher(ctx context.Context, id int64) {
	if id == 0 {
		return
	}
	if err := m.db.DetachAttacher(ctx, id); err != nil {
		m.logger.Error("detach attacher", "err", err)
	}
}

// RecordFilterEvent persists a command-filter event (also auditable).
func (m *Manager) RecordFilterEvent(ctx context.Context, sessionDBID int64, ruleID, severity, action, source, label, rawLine string) {
	lbl := label
	if err := m.db.InsertCommandFilterEvent(ctx, store.InsertCommandFilterEventParams{
		SessionID: sessionDBID, RuleID: ruleID, Severity: severity, ActionTaken: action,
		Source: source, AttacherLabel: &lbl, RawLine: rawLine,
	}); err != nil {
		m.logger.Error("record filter event", "err", err)
	}
}

func dialSSH(host string, port int32, user, secret, authKind string, timeout time.Duration) (*ssh.Client, error) {
	var auth ssh.AuthMethod
	switch authKind {
	case "password":
		auth = ssh.Password(secret)
	case "key":
		signer, err := ssh.ParsePrivateKey([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		auth = ssh.PublicKeys(signer)
	default:
		return nil, fmt.Errorf("unknown auth_kind %q", authKind)
	}
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{auth},
		// MVP: TOFU/known-hosts pinning is a hardening (docs/07 §7.8.2); the
		// network path is already constrained to the Agent overlay in M3/M4.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}
	return ssh.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))), cfg)
}
