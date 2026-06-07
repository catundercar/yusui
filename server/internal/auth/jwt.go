package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenKind distinguishes short-lived access tokens from long-lived refresh tokens.
type TokenKind string

const (
	AccessToken  TokenKind = "access"
	RefreshToken TokenKind = "refresh"
)

// Claims is the YuSui JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	Kind     TokenKind `json:"kind"`
	Username string    `json:"username"`
	Role     string    `json:"role"`
	// StepUpAt is the unix time of the last successful step-up (password/TOTP)
	// re-auth, carried on access tokens. Sensitive actions require it to be recent.
	StepUpAt int64 `json:"stepup_at,omitempty"`
}

// Manager issues and verifies tokens with a single HMAC secret.
type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewManager constructs a token manager.
func NewManager(secret string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL, now: time.Now}
}

func (m *Manager) issue(kind TokenKind, userID int64, username, role string, ttl time.Duration, stepUpAt int64) (string, error) {
	now := m.now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			Issuer:    "yusui",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Kind:     kind,
		Username: username,
		Role:     role,
		StepUpAt: stepUpAt,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign: %w", err)
	}
	return signed, nil
}

// IssueAccess mints an access token; stepUpAt records the last re-auth time.
func (m *Manager) IssueAccess(userID int64, username, role string, stepUpAt int64) (string, error) {
	return m.issue(AccessToken, userID, username, role, m.accessTTL, stepUpAt)
}

// IssueRefresh mints a refresh token.
func (m *Manager) IssueRefresh(userID int64, username, role string) (string, error) {
	return m.issue(RefreshToken, userID, username, role, m.refreshTTL, 0)
}

// Parse verifies the signature and expiry and returns the claims.
func (m *Manager) Parse(token string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: parse: %w", err)
	}
	return claims, nil
}
