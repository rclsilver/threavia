package api

import (
	"github.com/rclsilver/threavia/internal/core/auth"
	"net/http"
)

func (h *handler) registerMonitors(mux *http.ServeMux) {
	for _, route := range []struct {
		pattern string
		session bool
	}{{"GET /api/v1/sessions/{sessionId}/monitors", true}, {"GET /api/v1/projects/{projectId}/monitors", false}} {
		h.handle(mux, route.pattern, func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
			values, err := h.svc.ListMonitors(r.Context(), i, r.PathValue("sessionId"), r.PathValue("projectId"))
			if err != nil {
				h.fail(w, err)
				return
			}
			writeList(w, values)
		})
	}
	h.handle(mux, "POST /api/v1/monitors/{monitorId}/stop", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		if err := h.svc.StopMonitor(r.Context(), i, r.PathValue("monitorId")); err != nil {
			h.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
