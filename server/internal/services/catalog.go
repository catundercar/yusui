// Package services holds the application Service layer: thin orchestration with
// validation over the store. Services do not call each other directly; they
// coordinate ticket state through the Policy Engine (docs/01 §1.5).
package services

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/catundercar/yusui/server/internal/secrets"
	"github.com/catundercar/yusui/server/internal/store"
)

// ErrValidation marks a caller input error (maps to HTTP 400).
type ErrValidation struct{ msg string }

func (e ErrValidation) Error() string { return e.msg }

func invalid(format string, a ...any) error { return ErrValidation{msg: fmt.Sprintf(format, a...)} }

// Catalog manages projects, agents, assets and their credentials.
type Catalog struct {
	q      *store.Queries
	sealer *secrets.Sealer
}

// NewCatalog constructs the catalog service.
func NewCatalog(q *store.Queries, sealer *secrets.Sealer) *Catalog {
	return &Catalog{q: q, sealer: sealer}
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

func (c *Catalog) ListAgents(ctx context.Context) ([]store.YusuiAgent, error) {
	return c.q.ListAgents(ctx)
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

// IsValidation reports whether err is a caller-input validation error.
func IsValidation(err error) bool {
	var v ErrValidation
	return errors.As(err, &v)
}
