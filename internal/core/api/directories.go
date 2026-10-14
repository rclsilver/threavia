package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) updateDirectory(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		GitRemote   *string `json:"gitRemote"`
	}](w, r)
	if !ok {
		return
	}
	dir, err := h.svc.UpdateKnownDirectory(r.Context(), identity, domain.KnownDirectoryID(r.PathValue("directoryId")), body.Name, body.Description, body.GitRemote)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dir)
}

func (h *handler) deleteDirectory(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.DeleteKnownDirectory(r.Context(), identity, domain.KnownDirectoryID(r.PathValue("directoryId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) unbindDirectory(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.UnbindKnownDirectory(r.Context(), identity, domain.KnownDirectoryID(r.PathValue("directoryId")), domain.BackendInstanceID(r.PathValue("backendId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
