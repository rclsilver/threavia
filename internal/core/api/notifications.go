package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerNotifications(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/me/notifications", h.notifications)
	h.handle(mux, "POST /api/v1/notifications/read", h.markNotificationsRead)
	h.handle(mux, "POST /api/v1/notifications/{notificationId}/read", h.markNotificationRead)
}

// notifications lists what agents raised for the person, WARNING first.
func (h *handler) notifications(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	list, err := h.svc.Notifications(r.Context(), identity, queryBool(r, "unread"), domain.SessionID(r.URL.Query().Get("sessionId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, list)
}

func (h *handler) markNotificationRead(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.MarkNotificationRead(r.Context(), identity, domain.NotificationID(r.PathValue("notificationId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// markNotificationsRead marks everything read, or only a Session's. The body
// is optional: a client clearing everything sends none.
func (h *handler) markNotificationsRead(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	var body struct {
		SessionID domain.SessionID `json:"sessionId"`
	}
	if r.ContentLength != 0 {
		var ok bool
		if body, ok = decode[struct {
			SessionID domain.SessionID `json:"sessionId"`
		}](w, r); !ok {
			return
		}
	}
	if err := h.svc.MarkNotificationsRead(r.Context(), identity, body.SessionID); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
