package api

import (
	"net/http"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// sessionUsage ranks the caller's Sessions by what they consumed, per backend,
// model and effort.
func (h *handler) sessionUsage(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	query := r.URL.Query()
	from, to, rows, err := h.svc.SessionUsage(r.Context(), identity, domain.ProjectID(query.Get("projectId")),
		query.Get("from"), query.Get("to"), queryInt(r, "limit", 0))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		From string            `json:"from"`
		To   string            `json:"to"`
		Rows []domain.UsageRow `json:"rows"`
	}{from.Format(time.DateOnly), to.Format(time.DateOnly), rows})
}
