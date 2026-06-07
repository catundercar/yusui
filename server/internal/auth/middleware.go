package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ctxKey int

const principalKey ctxKey = iota

// Principal is the authenticated caller, derived from a verified access token.
type Principal struct {
	UserID   int64
	Username string
	Role     string
	StepUpAt int64
}

// PrincipalFrom extracts the principal placed by Authenticator.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// Authenticator verifies the Bearer access token and injects the Principal.
func Authenticator(m *Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearer(r)
			if tok == "" {
				writeAuthErr(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			claims, err := m.Parse(tok)
			if err != nil || claims.Kind != AccessToken {
				writeAuthErr(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			uid, err := strconv.ParseInt(claims.Subject, 10, 64)
			if err != nil {
				writeAuthErr(w, http.StatusUnauthorized, "invalid token subject")
				return
			}
			p := Principal{UserID: uid, Username: claims.Username, Role: claims.Role, StepUpAt: claims.StepUpAt}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
		})
	}
}

// RequireRole rejects callers whose role is not in the allowed set.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				writeAuthErr(w, http.StatusUnauthorized, "unauthenticated")
				return
			}
			if _, ok := allowed[p.Role]; !ok {
				writeAuthErr(w, http.StatusForbidden, "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireStepUp rejects callers whose last step-up is older than window
// (docs/07 §7.5: approve/revoke/admin actions need recent re-auth).
func RequireStepUp(window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				writeAuthErr(w, http.StatusUnauthorized, "unauthenticated")
				return
			}
			if p.StepUpAt == 0 || time.Since(time.Unix(p.StepUpAt, 0)) > window {
				writeAuthErr(w, http.StatusForbidden, "step-up re-auth required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// writeAuthErr writes a minimal JSON error without importing httpapi (avoids a
// cycle: httpapi depends on auth, not the other way around).
func writeAuthErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":` + strconv.Quote(msg) + `}`))
}
