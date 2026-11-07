package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// instructionsBody is a PUT of one level's instructions. Both fields are
// required: a PUT replaces both texts, so a client that only knows about one of
// them would otherwise wipe the other, such as a project agent text written
// long ago through the project form.
type instructionsBody struct {
	Agent       *string `json:"agent"`
	Supervision *string `json:"supervision"`
}

// decodeInstructions reads an instructionsBody and refuses one missing a field.
func decodeInstructions(w http.ResponseWriter, r *http.Request) (domain.Instructions, bool) {
	body, ok := decode[instructionsBody](w, r)
	if !ok {
		return domain.Instructions{}, false
	}
	if body.Agent == nil || body.Supervision == nil {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"both agent and supervision are required; send an empty text to clear one")
		return domain.Instructions{}, false
	}
	return domain.Instructions{Agent: *body.Agent, Supervision: *body.Supervision}, true
}

func (h *handler) getInstanceInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	view, err := h.svc.InstanceInstructions(r.Context(), identity)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setInstanceInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decodeInstructions(w, r)
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
	body, ok := decodeInstructions(w, r)
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
	body, ok := decodeInstructions(w, r)
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
