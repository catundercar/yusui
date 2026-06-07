package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/catundercar/yusui/server/internal/services"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CatalogHandler serves admin CRUD for projects, agents, assets, credentials.
type CatalogHandler struct {
	cat    *services.Catalog
	logger *slog.Logger
}

// NewCatalogHandler wires the catalog handlers.
func NewCatalogHandler(cat *services.Catalog, logger *slog.Logger) *CatalogHandler {
	return &CatalogHandler{cat: cat, logger: logger}
}

func (h *CatalogHandler) fail(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	switch {
	case services.IsValidation(err):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &pg) && pg.Code == "23505":
		writeErr(w, http.StatusConflict, "resource already exists")
	default:
		h.logger.Error("catalog handler error", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

func idParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil
}

// ---- projects ----

func (h *CatalogHandler) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code  string   `json:"code"`
		Name  string   `json:"name"`
		Cidrs []string `json:"cidrs"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := h.cat.CreateProject(r.Context(), req.Code, req.Name, req.Cidrs)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *CatalogHandler) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := h.cat.ListProjects(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (h *CatalogHandler) getProject(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := h.cat.GetProject(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ---- agents ----

func (h *CatalogHandler) createAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID int64  `json:"project_id"`
		Role      string `json:"role"`
		Hostname  string `json:"hostname"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := h.cat.CreateAgent(r.Context(), req.ProjectID, req.Role, req.Hostname)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *CatalogHandler) listAgents(w http.ResponseWriter, r *http.Request) {
	as, err := h.cat.ListAgents(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, as)
}

// ---- assets ----

func (h *CatalogHandler) createAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID  int64   `json:"project_id"`
		Name       string  `json:"name"`
		IPInternal string  `json:"ip_internal"`
		Ports      []int32 `json:"ports"`
		OS         *string `json:"os"`
		Tags       []byte  `json:"tags"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := h.cat.CreateAsset(r.Context(), req.ProjectID, req.Name, req.IPInternal, req.Ports, req.OS, req.Tags)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *CatalogHandler) listAssets(w http.ResponseWriter, r *http.Request) {
	as, err := h.cat.ListAssets(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, as)
}

func (h *CatalogHandler) getAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := h.cat.GetAsset(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// ---- asset credentials ----

func (h *CatalogHandler) createCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid asset id")
		return
	}
	var req struct {
		SSHUser     string  `json:"ssh_user"`
		AuthKind    string  `json:"auth_kind"`
		Secret      string  `json:"secret"`
		Fingerprint *string `json:"fingerprint"`
		Description *string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.cat.CreateCredential(r.Context(), id, req.SSHUser, req.AuthKind, req.Secret, req.Fingerprint, req.Description)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c) // RETURNING excludes secret_enc
}

func (h *CatalogHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid asset id")
		return
	}
	cs, err := h.cat.ListCredentials(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// ---- users ----

func (h *CatalogHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string  `json:"username"`
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
		Role        string  `json:"role"`
		Password    string  `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	u, err := h.cat.CreateUser(r.Context(), req.Username, req.DisplayName, req.Email, req.Role, req.Password)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPublicUser(u)) // never echo the hash
}

func (h *CatalogHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := h.cat.ListUsers(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, us)
}
