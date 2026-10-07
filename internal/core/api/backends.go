package api

import (
	"net/http"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func (h *handler) registerBackends(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/backends", h.listBackends)
	h.handle(mux, "GET /api/v1/backends/{backendId}", h.getBackend)
	h.handle(mux, "POST /api/v1/backends/{backendId}/revoke", h.revokeBackend)
	h.handle(mux, "POST /api/v1/backends/claim", h.claimBackend)
	h.handle(mux, "POST /api/v1/backend-tokens", h.createBackendToken)
}

// registerRegistration mounts the one route a backend calls before it has any
// user credential. It is gated by a one-shot registration token or by the shared
// registration key, never by user authentication.
func (h *handler) registerRegistration(mux *http.ServeMux) {
	h.open(mux, "POST /api/v1/backends/register", h.registerBackend)
}

func (h *handler) listBackends(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	instances, err := h.svc.ListBackendInstances(r.Context(), identity)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, instances)
}

func (h *handler) getBackend(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	instance, err := h.svc.GetBackendInstance(r.Context(), identity, domain.BackendInstanceID(r.PathValue("backendId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instance)
}

// revokeBackend invalidates the persistent credential. The record is kept for
// historical references and a returning backend must register anew.
func (h *handler) revokeBackend(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.RevokeBackendInstance(r.Context(), identity, domain.BackendInstanceID(r.PathValue("backendId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) claimBackend(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		ClaimCode string `json:"claimCode"`
	}](w, r)
	if !ok {
		return
	}

	instance, err := h.svc.ClaimBackend(r.Context(), identity, body.ClaimCode)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, instance)
}

// createBackendToken issues a one-shot registration token. The plaintext is
// returned once and never again.
func (h *handler) createBackendToken(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Label      string `json:"label"`
		TTLSeconds int    `json:"ttlSeconds"`
	}](w, r)
	if !ok {
		return
	}

	token, expiresAt, err := h.svc.CreateRegistrationToken(r.Context(), identity,
		body.Label, time.Duration(body.TTLSeconds)*time.Second)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     token,
		"expiresAt": expiresAt,
	})
}

// registerBackend creates a BackendInstance and issues its persistent
// credential. The response carries the credential once; Core keeps only a hash.
func (h *handler) registerBackend(w http.ResponseWriter, r *http.Request) {
	body, ok := decode[struct {
		Name         string   `json:"name"`
		Capabilities []string `json:"capabilities"`
		Token        string   `json:"token"`
		SharedKey    string   `json:"sharedKey"`
	}](w, r)
	if !ok {
		return
	}

	capabilities := make([]domain.Capability, 0, len(body.Capabilities))
	for _, capability := range body.Capabilities {
		capabilities = append(capabilities, domain.Capability(capability))
	}

	result, err := h.svc.RegisterBackend(r.Context(), service.RegisterBackendInput{
		Name:         body.Name,
		Capabilities: capabilities,
		Token:        body.Token,
		SharedKey:    body.SharedKey,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
