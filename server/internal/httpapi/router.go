// Package httpapi is the API Gateway layer (docs/01 §1.1): chi router,
// cross-cutting middleware, and HTTP handlers. Business logic lives behind
// Services/Engines and is wired in from M1 onward.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Readiness is the minimal DB-health dependency the router needs.
// Satisfied by *store.DB via the sqlc-generated HealthCheck.
type Readiness interface {
	HealthCheck(ctx context.Context) (int32, error)
}

// NewRouter builds the HTTP handler.
func NewRouter(ready Readiness, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// NOTE: chi's middleware.RealIP is deprecated (X-Forwarded-For spoofing).
	// Client-IP capture for audit will use a trusted-proxy-aware parser once
	// the reverse proxy is in front (docs/07 §7.4); deferred to M1+.
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(ready))
	return r
}
