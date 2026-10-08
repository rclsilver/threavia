package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func (h *handler) registerSchedules(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/sessions/{sessionId}/schedules", h.listSchedules)
	h.handle(mux, "POST /api/v1/sessions/{sessionId}/schedules", h.createSchedule)
	h.handle(mux, "PATCH /api/v1/schedules/{scheduleId}", h.updateSchedule)
	h.handle(mux, "DELETE /api/v1/schedules/{scheduleId}", h.deleteSchedule)
	h.handle(mux, "POST /api/v1/schedules/{scheduleId}/run", h.runSchedule)
}

func (h *handler) listSchedules(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	schedules, err := h.svc.ListSchedules(r.Context(), identity, domain.SessionID(r.PathValue("sessionId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, schedules)
}

func (h *handler) createSchedule(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[service.ScheduleInput](w, r)
	if !ok {
		return
	}
	created, err := h.svc.CreateSchedule(r.Context(), identity, domain.SessionID(r.PathValue("sessionId")), body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateSchedule(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[service.ScheduleInput](w, r)
	if !ok {
		return
	}
	updated, err := h.svc.UpdateSchedule(r.Context(), identity, domain.ScheduleID(r.PathValue("scheduleId")), body)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteSchedule(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.DeleteSchedule(r.Context(), identity, domain.ScheduleID(r.PathValue("scheduleId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) runSchedule(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	job, err := h.svc.RunScheduleNow(r.Context(), identity, domain.ScheduleID(r.PathValue("scheduleId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}
