// Package httpapi is the API Gateway layer (docs/01 §1.1): chi router,
// cross-cutting middleware, and HTTP handlers. Business logic lives behind
// Services/Engines.
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
	r.Use(requestLogger(d.Logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(d.Ready))

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", d.Auth.login)
		r.Post("/auth/refresh", d.Auth.refresh)
		// WS terminal self-authenticates via ?access_token (browsers can't set
		// headers on WS), so it sits outside the header-auth group.
		r.Get("/ws/tickets/{id}/terminal", d.WebShell.terminal)

		r.Group(func(r chi.Router) {
			r.Use(auth.Authenticator(d.Manager))
			r.Get("/me", d.Auth.me)
			r.Post("/auth/stepup", d.Auth.stepup)

			// Catalog reads — any authenticated user (requesters need these to
			// pick a project/asset when submitting a ticket).
			r.Get("/projects", d.Catalog.listProjects)
			r.Get("/projects/{id}", d.Catalog.getProject)
			r.Get("/agents", d.Catalog.listAgents)
			r.Get("/assets", d.Catalog.listAssets)
			r.Get("/assets/{id}", d.Catalog.getAsset)

			// Tickets.
			r.Route("/tickets", func(r chi.Router) {
				r.Post("/", d.Ticket.submit)
				r.Get("/", d.Ticket.list)
				r.Get("/{id}", d.Ticket.get)
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("approver", "admin"))
					r.Use(auth.RequireStepUp(d.StepUpWindow))
					r.Post("/{id}/approve", d.Ticket.approve)
					r.Post("/{id}/reject", d.Ticket.reject)
				})
				r.With(auth.RequireStepUp(d.StepUpWindow)).Post("/{id}/revoke", d.Ticket.revoke)
			})

			// Admin writes (docs/07 §7.5).
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Post("/projects", d.Catalog.createProject)
				r.Post("/agents", d.Catalog.createAgent)
				r.Post("/assets", d.Catalog.createAsset)
				r.Post("/assets/{id}/credentials", d.Catalog.createCredential)
				r.Get("/assets/{id}/credentials", d.Catalog.listCredentials)
				r.Post("/users", d.Catalog.createUser)
				r.Get("/users", d.Catalog.listUsers)
			})
		})
	})
	return r
}
