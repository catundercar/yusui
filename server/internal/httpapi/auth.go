package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/catundercar/yusui/server/internal/store"
)

// AuthHandler serves the authentication endpoints.
type AuthHandler struct {
	idp auth.IdentityAdapter
	mgr *auth.Manager
	q   *store.Queries
}

// NewAuthHandler wires the auth handlers.
func NewAuthHandler(idp auth.IdentityAdapter, mgr *auth.Manager, q *store.Queries) *AuthHandler {
	return &AuthHandler{idp: idp, mgr: mgr, q: q}
}

// publicUser is the user shape safe to return to clients (no hashes/secrets).
type publicUser struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	Role        string  `json:"role"`
	MfaEnabled  bool    `json:"mfa_enabled"`
	IsActive    bool    `json:"is_active"`
}

func toPublicUser(u store.YusuiUser) publicUser {
	return publicUser{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email,
		Role: u.Role, MfaEnabled: u.MfaEnabled, IsActive: u.IsActive,
	}
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		MfaCode  string `json:"mfa_code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	u, err := h.idp.Login(r.Context(), req.Username, req.Password, req.MfaCode)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountLocked):
			writeErr(w, http.StatusLocked, "account locked, try again later")
		case errors.Is(err, auth.ErrInactive):
			writeErr(w, http.StatusForbidden, "account inactive")
		case errors.Is(err, auth.ErrMFAUnsupported):
			writeErr(w, http.StatusNotImplemented, "MFA not yet supported")
		default:
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
		}
		return
	}
	now := time.Now().Unix() // a fresh login counts as a step-up
	access, err := h.mgr.IssueAccess(u.ID, u.Username, u.Role, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token error")
		return
	}
	refresh, err := h.mgr.IssueRefresh(u.ID, u.Username, u.Role)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "refresh_token": refresh, "user": toPublicUser(u),
	})
}

func (h *AuthHandler) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	claims, err := h.mgr.Parse(req.RefreshToken)
	if err != nil || claims.Kind != auth.RefreshToken {
		writeErr(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	uid, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	u, err := h.q.GetUserByID(r.Context(), uid)
	if err != nil || !u.IsActive {
		writeErr(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	// Refresh yields an access token WITHOUT step-up; sensitive actions re-step-up.
	access, err := h.mgr.IssueAccess(u.ID, u.Username, u.Role, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access})
}

func (h *AuthHandler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	u, err := h.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, toPublicUser(u))
}

func (h *AuthHandler) stepup(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Password string `json:"password"`
		MfaCode  string `json:"mfa_code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.idp.StepUp(r.Context(), p.UserID, req.Password, req.MfaCode); err != nil {
		writeErr(w, http.StatusUnauthorized, "step-up re-auth failed")
		return
	}
	access, err := h.mgr.IssueAccess(p.UserID, p.Username, p.Role, time.Now().Unix())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access})
}
