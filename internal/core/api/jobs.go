package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerJobs(mux *http.ServeMux) {
	h.handle(mux, "POST /api/v1/jobs/{jobId}/cancel", h.cancelJob)
	h.handle(mux, "DELETE /api/v1/jobs/{jobId}", h.deleteQueuedJob)
}

// cancelJob asks for a Job to stop. A live Job goes to CANCELLING and only
// reaches CANCELLED once the backend confirms the stop.
func (h *handler) cancelJob(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Reason string `json:"reason"`
	}](w, r)
	if !ok {
		return
	}

	job, err := h.svc.CancelJob(r.Context(), identity, domain.JobID(r.PathValue("jobId")), body.Reason)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// deleteQueuedJob removes a Job that has not started. A running one is cancelled
// instead.
func (h *handler) deleteQueuedJob(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.DeleteQueuedJob(r.Context(), identity, domain.JobID(r.PathValue("jobId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
