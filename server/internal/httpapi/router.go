// Package httpapi is the API Gateway layer (docs/01 §1.1): chi router,
// cross-cutting middleware, and HTTP handlers. Business logic lives behind
// Services/Engines and is wired in from M1 onward.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Readiness is the minimal DB-health dependency the router needs.
// Satisfied by *store.DB via the sqlc-generated HealthCheck.
type Readiness interface {
	HealthCheck(ctx context.Context) (int32, error)
}

// Deps are the collaborators the router wires together.
type Deps struct {
	Ready   Readiness
	Logger  *slog.Logger
	Auth    *AuthHandler
	Manager *auth.Manager
}

// NewRouter builds the HTTP handler.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// NOTE: chi's middleware.RealIP is deprecated (X-Forwarded-For spoofing).
	// Client-IP capture for audit will use a trusted-proxy-aware parser once
	// the reverse proxy is in front (docs/07 §7.4); deferred to M1+.
	r.Use(requestLogger(d.Logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(d.Ready))

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", d.Auth.login)
		r.Post("/auth/refresh", d.Auth.refresh)

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(auth.Authenticator(d.Manager))
			r.Get("/me", d.Auth.me)
			r.Post("/auth/stepup", d.Auth.stepup)
		})
	})
	return r
}
