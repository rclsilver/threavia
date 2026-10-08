package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func (h *handler) registerPush(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/me/push", h.pushConfig)
	h.handle(mux, "POST /api/v1/me/push/subscriptions", h.subscribePush)
	h.handle(mux, "DELETE /api/v1/me/push/subscriptions/{subscriptionId}", h.unsubscribePush)
	h.handle(mux, "POST /api/v1/me/push/test", h.testPush)
	h.registerPresence(mux)
}

func (h *handler) pushConfig(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	config, err := h.svc.PushConfig(r.Context(), identity)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (h *handler) subscribePush(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[service.PushSubscriptionInput](w, r)
	if !ok {
		return
	}
	// The page the browser subscribed from: what push services are told
	// to reach about this sender.
	sub, err := h.svc.SubscribePush(r.Context(), identity, body, r.Header.Get("Origin"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

func (h *handler) unsubscribePush(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.UnsubscribePush(r.Context(), identity,
		domain.PushSubscriptionID(r.PathValue("subscriptionId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) testPush(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	sent, err := h.svc.TestPush(r.Context(), identity)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"browsers": sent})
}

func (h *handler) registerPresence(mux *http.ServeMux) {
	h.handle(mux, "POST /api/v1/me/presence", h.setPresence)
	h.handle(mux, "GET /api/v1/me/clients", h.connectedClients)
}

// setPresence records whether the person is looking at the client that
// sends it, which decides whether their other devices are notified.
func (h *handler) setPresence(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Active bool `json:"active"`
	}](w, r)
	if !ok {
		return
	}
	if err := h.svc.SetPresence(r.Context(), identity, body.Active); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) connectedClients(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	writeList(w, h.svc.ConnectedClients(identity))
}
