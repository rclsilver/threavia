package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) getInstanceInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.InstanceInstructions(r.Context(), identity)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setInstanceInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[domain.Instructions](w, r)
	if !ok {
		return
	}
	view, err := h.svc.SetInstanceInstructions(r.Context(), identity, body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) getProjectInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.ProjectInstructions(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setProjectInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[domain.Instructions](w, r)
	if !ok {
		return
	}
	view, err := h.svc.SetProjectInstructions(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")), body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) getBackendInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.BackendInstructions(r.Context(), identity, domain.BackendInstanceID(r.PathValue("backendId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setBackendInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[domain.Instructions](w, r)
	if !ok {
		return
	}
	view, err := h.svc.SetBackendInstructions(r.Context(), identity, domain.BackendInstanceID(r.PathValue("backendId")), body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
