package api

import (
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"net/http"
)

func (h *handler) registerMCP(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/me/mcp", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		v, e := h.svc.ListMCP(r.Context(), i)
		if e != nil {
			h.fail(w, e)
			return
		}
		writeList(w, v)
	})
	h.handle(mux, "POST /api/v1/me/mcp", h.saveMCP)
	h.handle(mux, "PUT /api/v1/mcp/{serverId}", h.saveMCP)
	h.handle(mux, "DELETE /api/v1/mcp/{serverId}", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		if e := h.svc.DeleteMCP(r.Context(), i, r.PathValue("serverId")); e != nil {
			h.fail(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h.handle(mux, "GET /api/v1/projects/{projectId}/mcp", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		v, e := h.svc.MCPBindings(r.Context(), i, domain.ProjectID(r.PathValue("projectId")))
		if e != nil {
			h.fail(w, e)
			return
		}
		writeList(w, v)
	})
	h.handle(mux, "PUT /api/v1/projects/{projectId}/mcp", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		v, ok := decode[mcpconfig.Binding](w, r)
		if !ok {
			return
		}
		if e := h.svc.BindMCP(r.Context(), i, domain.ProjectID(r.PathValue("projectId")), v); e != nil {
			h.fail(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h.handle(mux, "GET /api/v1/sessions/{sessionId}/mcp", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		v, e := h.svc.MCPRestriction(r.Context(), i, domain.SessionID(r.PathValue("sessionId")))
		if e != nil {
			h.fail(w, e)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	h.handle(mux, "PUT /api/v1/sessions/{sessionId}/mcp", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		v, ok := decode[struct {
			ServerIDs []string `json:"serverIds"`
		}](w, r)
		if !ok {
			return
		}
		if e := h.svc.RestrictMCP(r.Context(), i, domain.SessionID(r.PathValue("sessionId")), v.ServerIDs); e != nil {
			h.fail(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h.handle(mux, "POST /api/v1/backends/{backendId}/mcp", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		v, ok := decode[struct {
			Operation   string `json:"operation"`
			ServerID    string `json:"serverId"`
			Source      string `json:"source"`
			RedirectURI string `json:"redirectUri"`
			Response    string `json:"response"`
			State       string `json:"state"`
		}](w, r)
		if !ok {
			return
		}
		out, e := h.svc.MCPBackend(r.Context(), i, domain.BackendInstanceID(r.PathValue("backendId")), v.Operation, v.ServerID, map[string]any{"source": v.Source, "redirectUri": v.RedirectURI, "response": v.Response, "state": v.State})
		if e != nil {
			h.fail(w, e)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}
func (h *handler) saveMCP(w http.ResponseWriter, r *http.Request, i auth.Identity) {
	v, ok := decode[mcpconfig.Server](w, r)
	if !ok {
		return
	}
	create := r.Method == http.MethodPost
	v.ID = r.PathValue("serverId")
	out, e := h.svc.SaveMCP(r.Context(), i, v, create)
	if e != nil {
		h.fail(w, e)
		return
	}
	code := http.StatusOK
	if create {
		code = http.StatusCreated
	}
	writeJSON(w, code, out)
}
