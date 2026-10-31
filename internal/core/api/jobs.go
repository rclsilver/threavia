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
// reaches CANCELLED once the backend confirms the stop. With ifQueued, only a
// Job that has not started is cancelled, and one that has is a conflict.
func (h *handler) cancelJob(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Reason   string `json:"reason"`
		IfQueued bool   `json:"ifQueued"`
	}](w, r)
	if !ok {
		return
	}

	jobID := domain.JobID(r.PathValue("jobId"))
	cancel := h.svc.CancelJob
	if body.IfQueued {
		cancel = h.svc.WithdrawQueuedJob
	}
	job, err := cancel(r.Context(), identity, jobID, body.Reason)
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
