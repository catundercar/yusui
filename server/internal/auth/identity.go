package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/catundercar/yusui/server/internal/store"
)

// Sentinel auth errors. Login returns a uniform ErrInvalidCredentials for
// bad username/password to avoid leaking which one was wrong.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account locked")
	ErrInactive           = errors.New("account inactive")
	ErrMFAUnsupported     = errors.New("mfa enabled but not yet supported")
)

// IdentityAdapter abstracts the auth backend. v0.1 ships LocalProvider; v0.3
// adds OIDCProvider implementing the same interface (docs/07 §7.5).
type IdentityAdapter interface {
	Login(ctx context.Context, username, password, mfaCode string) (store.YusuiUser, error)
	StepUp(ctx context.Context, userID int64, password, mfaCode string) error
}

// LocalProvider authenticates against the local users table.
type LocalProvider struct {
	q   *store.Queries
	now func() time.Time
}

// NewLocalProvider constructs a local-account provider.
func NewLocalProvider(q *store.Queries) *LocalProvider {
	return &LocalProvider{q: q, now: time.Now}
}

// Login verifies credentials, enforces lockout, and updates login counters.
func (p *LocalProvider) Login(ctx context.Context, username, password, mfaCode string) (store.YusuiUser, error) {
	u, err := p.q.GetUserByUsername(ctx, username)
	if err != nil {
		return store.YusuiUser{}, ErrInvalidCredentials // do not distinguish "no such user"
	}
	if !u.IsActive {
		return store.YusuiUser{}, ErrInactive
	}
	if u.LockedUntil.Valid && u.LockedUntil.Time.After(p.now()) {
		return store.YusuiUser{}, ErrAccountLocked
	}
	if u.PasswordHash == nil || !VerifyPassword(*u.PasswordHash, password) {
		_, _ = p.q.MarkLoginFailure(ctx, u.ID)
		return store.YusuiUser{}, ErrInvalidCredentials
	}
	// TOTP is not implemented in M1 (needs KMS + enrollment flow). Fail closed:
	// an MFA-enabled account must never authenticate without its second factor.
	if u.MfaEnabled {
		_, _ = p.q.MarkLoginFailure(ctx, u.ID)
		return store.YusuiUser{}, ErrMFAUnsupported
	}
	if err := p.q.MarkLoginSuccess(ctx, u.ID); err != nil {
		return store.YusuiUser{}, fmt.Errorf("auth: mark login success: %w", err)
	}
	return u, nil
}

// StepUp re-verifies the user's password (and TOTP, once supported) for a
// sensitive action. It does not touch lockout counters.
func (p *LocalProvider) StepUp(ctx context.Context, userID int64, password, mfaCode string) error {
	u, err := p.q.GetUserByID(ctx, userID)
	if err != nil {
		return ErrInvalidCredentials
	}
	if u.PasswordHash == nil || !VerifyPassword(*u.PasswordHash, password) {
		return ErrInvalidCredentials
	}
	if u.MfaEnabled {
		return ErrMFAUnsupported
	}
	return nil
}
