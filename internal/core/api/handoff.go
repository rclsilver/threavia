package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) previewHandoff(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	id := r.URL.Query().Get("backendInstanceId")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "choose a backend identifier")
		return
	}
	preview, err := h.svc.PreviewHandoff(r.Context(), identity, domain.SessionID(r.PathValue("sessionId")), domain.BackendInstanceID(id))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
