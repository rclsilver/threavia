package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) getInstancePolicy(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.InstancePolicyOrigin(r.Context(), identity)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) getBackendPolicy(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.BackendPolicyOrigin(r.Context(), identity, domain.BackendInstanceID(r.PathValue("backendId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) getProjectPolicyOrigin(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.ProjectPolicyOrigin(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setInstancePolicy(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[domain.ExecutionPolicy](w, r)
	if !ok {
		return
	}
	var policy *domain.ExecutionPolicy
	if body.Mode != "" {
		policy = &body
	}
	view, err := h.svc.SetInstanceExecutionPolicy(r.Context(), identity, policy)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setBackendPolicy(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[domain.ExecutionPolicy](w, r)
	if !ok {
		return
	}
	var policy *domain.ExecutionPolicy
	if body.Mode != "" {
		policy = &body
	}
	view, err := h.svc.SetBackendExecutionPolicy(r.Context(), identity, domain.BackendInstanceID(r.PathValue("backendId")), policy)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
