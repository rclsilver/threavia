package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerAttention(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/me/attention", h.attention)
	h.handle(mux, "POST /api/v1/validations/{validationId}/resolve", h.resolveValidation)
	h.handle(mux, "POST /api/v1/user-input/{requestId}/resolve", h.resolveUserInput)
}

// attention returns what is currently waiting for the user. It is state, not a
// count of unread events, which is what lets a reconnecting client rebuild its
// notifications without replaying history.
func (h *handler) attention(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	pending, err := h.svc.Attention(r.Context(), identity, domain.SessionID(r.URL.Query().Get("sessionId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pending)
}

// resolveValidation records a permission decision. The first valid response
// wins; a second one is reported as a conflict.
func (h *handler) resolveValidation(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Approved bool   `json:"approved"`
		Note     string `json:"note"`
		Channel  string `json:"channel"`
	}](w, r)
	if !ok {
		return
	}

	resolved, err := h.svc.ResolveValidation(r.Context(), identity,
		domain.ValidationRequestID(r.PathValue("validationId")),
		body.Approved, resolvedChannel(r, body.Channel), body.Note)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resolved)
}

// resolveUserInput records an answer to a pending input request.
func (h *handler) resolveUserInput(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Value   string `json:"value"`
		Channel string `json:"channel"`
	}](w, r)
	if !ok {
		return
	}

	resolved, err := h.svc.ResolveUserInput(r.Context(), identity,
		domain.UserInputRequestID(r.PathValue("requestId")), body.Value, resolvedChannel(r, body.Channel))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resolved)
}

// resolvedChannel records which client answered, for the audit receipt. The
// body wins over the header, so a client that answers on behalf of another can
// say so.
func resolvedChannel(r *http.Request, declared string) string {
	if declared != "" {
		return domain.NormaliseChannel(declared).String()
	}
	return channel(r).String()
}
