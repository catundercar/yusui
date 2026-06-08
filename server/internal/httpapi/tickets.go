package httpapi

import (
	"errors"
	"net/http"

	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/catundercar/yusui/server/internal/policy"
	"github.com/catundercar/yusui/server/internal/store"
)

// TicketHandler serves ticket submit/list/get and approve/reject/revoke.
type TicketHandler struct {
	engine *policy.Engine
}

// NewTicketHandler wires the ticket handlers.
func NewTicketHandler(engine *policy.Engine) *TicketHandler { return &TicketHandler{engine: engine} }

func (h *TicketHandler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrSelfApprove): // must precede ErrForbidden (it wraps it)
		writeErrCode(w, http.StatusForbidden, "approver_eq_requester", err.Error())
	case errors.Is(err, policy.ErrValidation):
		writeErrCode(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, policy.ErrNotFound):
		writeErrCode(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
	case errors.Is(err, policy.ErrConflict):
		writeErrCode(w, http.StatusConflict, "ticket_conflict", err.Error())
	case errors.Is(err, policy.ErrForbidden):
		writeErrCode(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

func actorOf(p auth.Principal) policy.Actor { return policy.Actor{Type: "user", ID: p.Username} }

func (h *TicketHandler) submit(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		ProjectID   int64   `json:"project_id"`
		AssetIDs    []int64 `json:"asset_ids"`
		Ports       []int32 `json:"ports"`
		Protocol    string  `json:"protocol"`
		AccessKind  string  `json:"access_kind"`
		Reason      string  `json:"reason"`
		DurationSec int32   `json:"duration_sec"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := h.engine.Submit(r.Context(), policy.SubmitInput{
		RequesterID: p.UserID, ProjectID: req.ProjectID, AssetIDs: req.AssetIDs,
		Ports: req.Ports, Protocol: req.Protocol, AccessKind: req.AccessKind,
		Reason: req.Reason, DurationSec: req.DurationSec,
	}, actorOf(p))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *TicketHandler) list(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var (
		ts  []store.YusuiTicket
		err error
	)
	if p.Role == "admin" || p.Role == "approver" {
		ts, err = h.engine.List(r.Context())
	} else {
		ts, err = h.engine.ListByRequester(r.Context(), p.UserID)
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(ts))
}

func (h *TicketHandler) get(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := h.engine.Get(r.Context(), id)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	}
	if p.Role == "requester" && t.RequesterID != p.UserID {
		writeErrCode(w, http.StatusForbidden, "forbidden_not_owner", "not your ticket")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) approve(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := h.engine.Approve(r.Context(), id, p.UserID, actorOf(p))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) reject(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req)
	t, err := h.engine.Reject(r.Context(), id, p.UserID, req.Reason, actorOf(p))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) revoke(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	// Admin may revoke any ticket; a requester may revoke only their own.
	t, err := h.engine.Get(r.Context(), id)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, "ticket_not_found", "ticket not found")
		return
	}
	if p.Role != "admin" && t.RequesterID != p.UserID {
		writeErrCode(w, http.StatusForbidden, "forbidden_not_owner", "not allowed to revoke this ticket")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req)
	reason := req.Reason
	if reason == "" {
		reason = "manual revoke"
	}
	if err := h.engine.Revoke(r.Context(), id, reason, actorOf(p)); err != nil {
		h.fail(w, err)
		return
	}
	updated, _ := h.engine.Get(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}
