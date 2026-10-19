package api

import (
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
	"net/http"
)

func (h *handler) registerReviews(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/sessions/{sessionId}/review", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		review, err := h.svc.ReadReview(r.Context(), i, domain.SessionID(r.PathValue("sessionId")))
		if err == nil && r.URL.Query().Get("compareTo") != "" {
			review, err = h.svc.ReviewComparison(r.Context(), i, review, r.URL.Query().Get("revisionId"), r.URL.Query().Get("compareTo"))
		}
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, review)
	})
	h.handle(mux, "POST /api/v1/sessions/{sessionId}/review", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		in, ok := decode[service.ReviewAction](w, r)
		if !ok {
			return
		}
		review, err := h.svc.ActOnReview(r.Context(), i, domain.SessionID(r.PathValue("sessionId")), in)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, review)
	})
	h.handle(mux, "GET /api/v1/sessions/{sessionId}/review/file", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		q := r.URL.Query()
		file, err := h.svc.ReviewFile(r.Context(), i, domain.SessionID(r.PathValue("sessionId")), q.Get("revisionId"), q.Get("repositoryId"), q.Get("path"), q.Get("compareTo"))
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, file)
	})
}
