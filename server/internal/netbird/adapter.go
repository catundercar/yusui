// Package netbird is the NetBird Adapter (docs/04): it translates YuSui domain
// events into NetBird Management REST calls and classifies errors so the
// Reconciler can decide retry vs. alert. It ONLY calls the Mgmt REST API — never
// reads NetBird's DB or patches its source.
//
// v0.1-draft6+: the per-ticket path does NOT touch NetBird. The adapter is used
// at startup (install the one permanent policy) and on project/agent register.
// Idempotency key is the resource `name` with a `yusui:` prefix (invariant #6).
//
// NOTE: the request/response shapes follow docs/04's documented contract; real
// NetBird-Mgmt contract tests (docs/04 §4.12) are required before production —
// the unit tests here cover request shaping + idempotency against a mock.
package netbird

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ErrClass lets the Reconciler decide retry vs. alert (docs/04 §4.8).
type ErrClass int

const (
	ErrTransient ErrClass = iota // 5xx / network → retry
	ErrConflict                  // 409 → reconcile
	ErrAuth                      // 401/403 → alert, stop
	ErrSchema                    // unrecognized fields → version mismatch
	ErrPermanent                 // other 4xx → mark failed
)

func (c ErrClass) String() string {
	return [...]string{"transient", "conflict", "auth", "schema", "permanent"}[c]
}

// APIError carries the classification.
type APIError struct {
	Class  ErrClass
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("netbird: %s error (http %d): %s", e.Class, e.Status, e.Msg)
}

// ClassOf extracts the ErrClass from an error (ErrPermanent if not an APIError).
func ClassOf(err error) ErrClass {
	var e *APIError
	if as(err, &e) {
		return e.Class
	}
	return ErrPermanent
}

func classify(status int) ErrClass {
	switch {
	case status >= 500:
		return ErrTransient
	case status == 401 || status == 403:
		return ErrAuth
	case status == 409:
		return ErrConflict
	case status >= 400:
		return ErrPermanent
	default:
		return ErrPermanent
	}
}

// Group / Policy are the subset of NetBird objects YuSui uses.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Policy struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Adapter is a thin NetBird Mgmt REST client.
type Adapter struct {
	base   string
	token  string
	hc     *http.Client
	logger *slog.Logger
}

// New constructs the adapter. token is a NetBird PAT (sent as "Token <token>").
func New(baseURL, token string, logger *slog.Logger) *Adapter {
	return &Adapter{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		hc:     &http.Client{Timeout: 10 * time.Second},
		logger: logger,
	}
}

func (a *Adapter) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return &APIError{Class: ErrPermanent, Msg: "marshal: " + err.Error()}
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rdr)
	if err != nil {
		return &APIError{Class: ErrPermanent, Msg: err.Error()}
	}
	req.Header.Set("Authorization", "Token "+a.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.hc.Do(req)
	if err != nil {
		return &APIError{Class: ErrTransient, Msg: err.Error()}
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return &APIError{Class: classify(res.StatusCode), Status: res.StatusCode, Msg: strings.TrimSpace(string(data))}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return &APIError{Class: ErrSchema, Status: res.StatusCode, Msg: "decode: " + err.Error()}
		}
	}
	return nil
}

// ListGroups returns all NetBird groups (also serves as a connectivity probe).
func (a *Adapter) ListGroups(ctx context.Context) ([]Group, error) {
	var gs []Group
	if err := a.do(ctx, http.MethodGet, "/api/groups", nil, &gs); err != nil {
		return nil, err
	}
	return gs, nil
}

// EnsureGroup returns the id of the group named name, creating it if absent.
func (a *Adapter) EnsureGroup(ctx context.Context, name string) (string, error) {
	gs, err := a.ListGroups(ctx)
	if err != nil {
		return "", err
	}
	for _, g := range gs {
		if g.Name == name {
			return g.ID, nil
		}
	}
	var created Group
	if err := a.do(ctx, http.MethodPost, "/api/groups", map[string]any{"name": name}, &created); err != nil {
		return "", err
	}
	a.logger.Info("netbird group created", "name", name, "id", created.ID)
	return created.ID, nil
}

// ListPolicies returns all NetBird policies.
func (a *Adapter) ListPolicies(ctx context.Context) ([]Policy, error) {
	var ps []Policy
	if err := a.do(ctx, http.MethodGet, "/api/policies", nil, &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// EnsureBuiltinPolicy ensures the single permanent policy exists (docs/04 §4.4):
// server-peers group -> agent groups, action accept. Idempotent by name.
func (a *Adapter) EnsureBuiltinPolicy(ctx context.Context, name, srcGroupID string, dstGroupIDs []string) (string, error) {
	ps, err := a.ListPolicies(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if p.Name == name {
			return p.ID, nil // MVP: presence check; field-diff/repair is a reconcile concern
		}
	}
	body := map[string]any{
		"name":        name,
		"description": "YuSui permanent policy: server-peer -> all agents; ports enforced by Agent nftables",
		"enabled":     true,
		"rules": []map[string]any{{
			"name":          name + ":r0",
			"sources":       []string{srcGroupID},
			"destinations":  dstGroupIDs,
			"protocol":      "all",
			"action":        "accept",
			"bidirectional": false,
			"enabled":       true,
		}},
	}
	var created Policy
	if err := a.do(ctx, http.MethodPost, "/api/policies", body, &created); err != nil {
		return "", err
	}
	a.logger.Info("netbird builtin policy created", "name", name, "id", created.ID)
	return created.ID, nil
}

// as is a tiny errors.As shim to avoid importing errors twice in hot paths.
func as(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
