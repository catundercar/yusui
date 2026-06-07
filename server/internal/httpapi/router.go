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
	Ready        Readiness
	Logger       *slog.Logger
	Auth         *AuthHandler
	Catalog      *CatalogHandler
	Ticket       *TicketHandler
	WebShell     *WebShellHandler
	Manager      *auth.Manager
	StepUpWindow time.Duration
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

			// Admin CRUD (docs/07 §7.5: project/asset/agent CRUD is admin-only).
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Route("/projects", func(r chi.Router) {
					r.Post("/", d.Catalog.createProject)
					r.Get("/", d.Catalog.listProjects)
					r.Get("/{id}", d.Catalog.getProject)
				})
				r.Route("/agents", func(r chi.Router) {
					r.Post("/", d.Catalog.createAgent)
					r.Get("/", d.Catalog.listAgents)
				})
				r.Route("/assets", func(r chi.Router) {
					r.Post("/", d.Catalog.createAsset)
					r.Get("/", d.Catalog.listAssets)
					r.Get("/{id}", d.Catalog.getAsset)
					r.Post("/{id}/credentials", d.Catalog.createCredential)
					r.Get("/{id}/credentials", d.Catalog.listCredentials)
				})
				r.Route("/users", func(r chi.Router) {
					r.Post("/", d.Catalog.createUser)
					r.Get("/", d.Catalog.listUsers)
				})
			})

			r.Route("/tickets", func(r chi.Router) {
				r.Post("/", d.Ticket.submit) // requester submits
				r.Get("/", d.Ticket.list)
				r.Get("/{id}", d.Ticket.get)
				r.Get("/{id}/terminal", d.WebShell.terminal) // WebSocket: open Web SSH
				// Approve/reject: approver or admin, with recent step-up re-auth.
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("approver", "admin"))
					r.Use(auth.RequireStepUp(d.StepUpWindow))
					r.Post("/{id}/approve", d.Ticket.approve)
					r.Post("/{id}/reject", d.Ticket.reject)
				})
				// Revoke: admin or the owning requester (checked in handler), step-up required.
				r.With(auth.RequireStepUp(d.StepUpWindow)).Post("/{id}/revoke", d.Ticket.revoke)
			})
		})
	})
	return r
}
