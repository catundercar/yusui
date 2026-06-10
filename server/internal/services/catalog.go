// Package services holds the application Service layer: thin orchestration with
// validation over the store. Services do not call each other directly; they
// coordinate ticket state through the Policy Engine (docs/01 §1.5).
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/catundercar/yusui/server/internal/secrets"
	"github.com/catundercar/yusui/server/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrValidation marks a caller input error (maps to HTTP 400).
type ErrValidation struct{ msg string }

func (e ErrValidation) Error() string { return e.msg }

func invalid(format string, a ...any) error { return ErrValidation{msg: fmt.Sprintf(format, a...)} }

// Catalog manages projects, agents, assets and their credentials.
type Catalog struct {
	db     *store.DB
	q      *store.Queries
	sealer *secrets.Sealer
}

// NewCatalog constructs the catalog service. It takes the full *store.DB so
// enrollment changes can be written with their audit row in one transaction
// (invariant #7); plain reads/writes still go through the embedded Queries.
func NewCatalog(db *store.DB, sealer *secrets.Sealer) *Catalog {
	return &Catalog{db: db, q: db.Queries, sealer: sealer}
}

// ---- projects ----

func (c *Catalog) CreateProject(ctx context.Context, code, name string, cidrs []string) (store.YusuiProject, error) {
	if code == "" || name == "" {
		return store.YusuiProject{}, invalid("code and name are required")
	}
	if len(cidrs) == 0 {
		return store.YusuiProject{}, invalid("at least one cidr is required")
	}
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, s := range cidrs {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return store.YusuiProject{}, invalid("invalid cidr %q", s)
		}
		prefixes = append(prefixes, p)
	}
	return c.q.CreateProject(ctx, store.CreateProjectParams{Code: code, Name: name, Cidrs: prefixes})
}

func (c *Catalog) ListProjects(ctx context.Context) ([]store.YusuiProject, error) {
	return c.q.ListProjects(ctx)
}

func (c *Catalog) GetProject(ctx context.Context, id int64) (store.YusuiProject, error) {
	return c.q.GetProjectByID(ctx, id)
}

// ---- agents ----

func (c *Catalog) CreateAgent(ctx context.Context, projectID int64, role, hostname string) (store.YusuiAgent, error) {
	if role != "primary" && role != "secondary" {
		return store.YusuiAgent{}, invalid("role must be 'primary' or 'secondary'")
	}
	if hostname == "" {
		return store.YusuiAgent{}, invalid("hostname is required")
	}
	if _, err := c.q.GetProjectByID(ctx, projectID); err != nil {
		return store.YusuiAgent{}, invalid("project %d not found", projectID)
	}
	return c.q.CreateAgent(ctx, store.CreateAgentParams{ProjectID: projectID, Role: role, Hostname: hostname})
}

// AgentView is the admin-facing agent shape: identical to the stored row but
// with the sensitive netbird_setup_key redacted to a boolean (docs/11 §11.7).
type AgentView struct {
	ID              int64              `json:"id"`
	ProjectID       int64              `json:"project_id"`
	Role            string             `json:"role"`
	Hostname        string             `json:"hostname"`
	NetbirdPeerID   *string            `json:"netbird_peer_id"`
	NetbirdRouteID  *string            `json:"netbird_route_id"`
	AgentVersion    *string            `json:"agent_version"`
	CertFingerprint *string            `json:"cert_fingerprint"`
	Status          string             `json:"status"`
	LastSeenAt      pgtype.Timestamptz `json:"last_seen_at"`
	RegisteredAt    pgtype.Timestamptz `json:"registered_at"`
	Enrollment      string             `json:"enrollment"`
	HasSetupKey     bool               `json:"has_setup_key"`
}

func agentView(a store.YusuiAgent) AgentView {
	return AgentView{
		ID: a.ID, ProjectID: a.ProjectID, Role: a.Role, Hostname: a.Hostname,
		NetbirdPeerID: a.NetbirdPeerID, NetbirdRouteID: a.NetbirdRouteID,
		AgentVersion: a.AgentVersion, CertFingerprint: a.CertFingerprint,
		Status: a.Status, LastSeenAt: a.LastSeenAt, RegisteredAt: a.RegisteredAt,
		Enrollment: a.Enrollment, HasSetupKey: a.NetbirdSetupKey != nil,
	}
}

func (c *Catalog) ListAgents(ctx context.Context) ([]AgentView, error) {
	rows, err := c.q.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, len(rows))
	for i, a := range rows {
		out[i] = agentView(a)
	}
	return out, nil
}

// ApproveAgent flips enrollment to approved, optionally binding a NetBird setup
// key (P2; P1 typically passes ""), and audits it in the same transaction.
func (c *Catalog) ApproveAgent(ctx context.Context, id int64, actorID, setupKey string) (AgentView, error) {
	return c.setEnrollment(ctx, id, actorID, "agent.approve", setupKey, true)
}

// RejectAgent flips enrollment to rejected and audits it in the same transaction.
func (c *Catalog) RejectAgent(ctx context.Context, id int64, actorID string) (AgentView, error) {
	return c.setEnrollment(ctx, id, actorID, "agent.reject", "", false)
}

func (c *Catalog) setEnrollment(ctx context.Context, id int64, actorID, action, setupKey string, approve bool) (AgentView, error) {
	tx, err := c.db.Pool.Begin(ctx)
	if err != nil {
		return AgentView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := c.db.WithTx(tx)

	var a store.YusuiAgent
	if approve {
		var key *string
		if setupKey != "" {
			key = &setupKey
		}
		a, err = q.ApproveAgent(ctx, store.ApproveAgentParams{ID: id, NetbirdSetupKey: key})
	} else {
		a, err = q.RejectAgent(ctx, id)
	}
	if err != nil {
		return AgentView{}, err
	}

	aid := actorID
	tt := "agent"
	tid := strconv.FormatInt(id, 10)
	payload, _ := json.Marshal(map[string]any{
		"hostname": a.Hostname, "project_id": a.ProjectID, "role": a.Role, "enrollment": a.Enrollment,
	})
	if err := q.InsertAudit(ctx, store.InsertAuditParams{
		ActorType: "user", ActorID: &aid, Action: action,
		TargetType: &tt, TargetID: &tid, Payload: payload,
	}); err != nil {
		return AgentView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentView{}, err
	}
	return agentView(a), nil
}

// ---- assets ----

func (c *Catalog) CreateAsset(ctx context.Context, projectID int64, name, ip string, ports []int32, os *string, tags []byte) (store.YusuiAsset, error) {
	if name == "" {
		return store.YusuiAsset{}, invalid("name is required")
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return store.YusuiAsset{}, invalid("invalid ip_internal %q", ip)
	}
	if _, err := c.q.GetProjectByID(ctx, projectID); err != nil {
		return store.YusuiAsset{}, invalid("project %d not found", projectID)
	}
	if len(tags) == 0 {
		tags = []byte("{}")
	}
	if ports == nil {
		ports = []int32{}
	}
	return c.q.CreateAsset(ctx, store.CreateAssetParams{
		ProjectID: projectID, Name: name, IpInternal: addr, Ports: ports, Os: os, Tags: tags,
	})
}

func (c *Catalog) ListAssets(ctx context.Context) ([]store.YusuiAsset, error) {
	return c.q.ListAssets(ctx)
}

func (c *Catalog) GetAsset(ctx context.Context, id int64) (store.YusuiAsset, error) {
	return c.q.GetAssetByID(ctx, id)
}

// ---- asset credentials ----

// CreateCredential seals the secret before storing it.
func (c *Catalog) CreateCredential(ctx context.Context, assetID int64, sshUser, authKind, secret string, fingerprint, description *string) (store.CreateAssetCredentialRow, error) {
	if sshUser == "" {
		return store.CreateAssetCredentialRow{}, invalid("ssh_user is required")
	}
	if authKind != "key" && authKind != "password" {
		return store.CreateAssetCredentialRow{}, invalid("auth_kind must be 'key' or 'password'")
	}
	if secret == "" {
		return store.CreateAssetCredentialRow{}, invalid("secret is required")
	}
	if _, err := c.q.GetAssetByID(ctx, assetID); err != nil {
		return store.CreateAssetCredentialRow{}, invalid("asset %d not found", assetID)
	}
	enc, err := c.sealer.Seal([]byte(secret))
	if err != nil {
		return store.CreateAssetCredentialRow{}, err
	}
	return c.q.CreateAssetCredential(ctx, store.CreateAssetCredentialParams{
		AssetID: assetID, SshUser: sshUser, AuthKind: authKind,
		SecretEnc: enc, SecretKmsKeyID: c.sealer.KeyID(),
		Fingerprint: fingerprint, Description: description,
	})
}

func (c *Catalog) ListCredentials(ctx context.Context, assetID int64) ([]store.ListCredentialsForAssetRow, error) {
	return c.q.ListCredentialsForAsset(ctx, assetID)
}

// OpenCredentialSecret fetches and decrypts the active credential for an asset
// (used by the Web Shell in M2). Returns ssh user, secret, auth kind.
func (c *Catalog) OpenCredentialSecret(ctx context.Context, assetID int64) (sshUser, secret, authKind string, err error) {
	cred, err := c.q.GetActiveCredentialForAsset(ctx, assetID)
	if err != nil {
		return "", "", "", fmt.Errorf("no active credential for asset %d: %w", assetID, err)
	}
	pt, err := c.sealer.Open(cred.SecretEnc)
	if err != nil {
		return "", "", "", err
	}
	return cred.SshUser, string(pt), cred.AuthKind, nil
}

// ---- users (admin-managed) ----

// CreateUser hashes the password (policy-checked) and creates a local account.
func (c *Catalog) CreateUser(ctx context.Context, username string, displayName, email *string, role, password string) (store.YusuiUser, error) {
	if username == "" {
		return store.YusuiUser{}, invalid("username is required")
	}
	switch role {
	case "requester", "approver", "admin":
	default:
		return store.YusuiUser{}, invalid("role must be requester|approver|admin")
	}
	if err := auth.CheckPolicy(password); err != nil {
		return store.YusuiUser{}, invalid("%s", err.Error())
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.YusuiUser{}, err
	}
	return c.q.CreateUser(ctx, store.CreateUserParams{
		Username: username, DisplayName: displayName, Email: email,
		Role: role, PasswordHash: &hash, MfaEnabled: false,
	})
}

// ListUsers returns all users without secrets.
func (c *Catalog) ListUsers(ctx context.Context) ([]store.ListUsersRow, error) {
	return c.q.ListUsers(ctx)
}

// IsValidation reports whether err is a caller-input validation error.
func IsValidation(err error) bool {
	var v ErrValidation
	return errors.As(err, &v)
}
