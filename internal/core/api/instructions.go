package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// instructionsBody is a PUT of one level's instructions. Both fields are
// required at the global and backend levels: a PUT replaces both texts, so a
// client that only knows about one of them would otherwise wipe the other. The
// project level may leave out the agent text, which it then keeps.
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

// setProjectInstructions may leave out the agent text, which is then kept as
// stored. The project agent text is also edited through the project form, so
// an editor open on an old copy that only changed the supervision must not
// send that copy back.
func (h *handler) setProjectInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[instructionsBody](w, r)
	if !ok {
		return
	}
	if body.Supervision == nil {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"supervision is required; send an empty text to clear it")
		return
	}
	view, err := h.svc.SetProjectInstructions(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")), body.Agent, *body.Supervision)
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

// previewInstructions answers what a Job started in a Project on a backend
// would read, and in one of its Sessions when sessionId is given. The backend
// is required: which one runs a Session is known per Job, and its level is part
// of the text.
func (h *handler) previewInstructions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	query := r.URL.Query()
	backendID := query.Get("backendId")
	if backendID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "backendId is required")
		return
	}
	var sessionID *domain.SessionID
	if value := query.Get("sessionId"); value != "" {
		id := domain.SessionID(value)
		sessionID = &id
	}
	preview, err := h.svc.PreviewInstructions(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), domain.BackendInstanceID(backendID), sessionID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
