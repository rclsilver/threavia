package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func (h *handler) registerSessions(mux *http.ServeMux) {
	h.handle(mux, "POST /api/v1/sessions/start", h.startSession)
	h.handle(mux, "GET /api/v1/sessions/{sessionId}", h.getSession)
	h.handle(mux, "PATCH /api/v1/sessions/{sessionId}", h.patchSession)
	h.handle(mux, "DELETE /api/v1/sessions/{sessionId}", h.deleteSession)
	h.handle(mux, "GET /api/v1/sessions/{sessionId}/events", h.sessionHistory)
	h.handle(mux, "POST /api/v1/sessions/{sessionId}/messages", h.postMessage)
	h.handle(mux, "POST /api/v1/sessions/{sessionId}/archive", h.archiveSession)
	h.handle(mux, "POST /api/v1/sessions/{sessionId}/restore", h.restoreSession)
}

func (h *handler) listSessions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	sessions, err := h.svc.ListSessions(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), queryBool(r, "includeArchived"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, sessions)
}

// startSession is the atomic first send of a client-side draft: it creates the
// Session, its first Run, its first Job and the first message, or nothing at
// all. An Idempotency-Key header makes a retry return the same Session.
func (h *handler) startSession(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		ProjectID          string  `json:"projectId"`
		BackendInstanceID  string  `json:"backendInstanceId"`
		WorkingDirectoryID *string `json:"workingDirectoryId"`
		Message            string  `json:"message"`
		NativeSessionID    *string `json:"nativeSessionId"`
	}](w, r)
	if !ok {
		return
	}

	in := service.StartSessionInput{
		ProjectID:         domain.ProjectID(body.ProjectID),
		BackendInstanceID: domain.BackendInstanceID(body.BackendInstanceID),
		Message:           body.Message,
		IdempotencyKey:    r.Header.Get("Idempotency-Key"),
	}
	if body.NativeSessionID != nil {
		in.NativeSessionID = *body.NativeSessionID
	}
	if body.WorkingDirectoryID != nil && *body.WorkingDirectoryID != "" {
		dirID := domain.KnownDirectoryID(*body.WorkingDirectoryID)
		in.WorkingDirectoryID = &dirID
	}

	result, err := h.svc.StartSession(r.Context(), identity, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// getSession returns the initial load of a Session: current state, a recent
// window of history, the pending attention items and the cursor to stream from.
func (h *handler) getSession(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	snapshot, err := h.svc.SessionSnapshot(r.Context(), identity,
		domain.SessionID(r.PathValue("sessionId")), queryInt(r, "history", 100))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *handler) patchSession(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Title *string `json:"title"`
		// WorkingDirectoryID is a double pointer so that an explicit null clears
		// the working directory, while an absent field leaves it alone.
		WorkingDirectoryID **string `json:"workingDirectoryId"`
		// BackendInstanceID moves the Session to another backend. It is an
		// explicit act: the timeline stays continuous, but the native provider
		// session does not travel (spec section 34).
		BackendInstanceID *string `json:"backendInstanceId"`
	}](w, r)
	if !ok {
		return
	}

	sessionID := domain.SessionID(r.PathValue("sessionId"))
	var (
		session domain.Session
		err     error
		changed bool
	)

	if body.Title != nil {
		session, err = h.svc.RenameSession(r.Context(), identity, sessionID, *body.Title)
		if err != nil {
			h.fail(w, err)
			return
		}
		changed = true
	}

	if body.BackendInstanceID != nil && *body.BackendInstanceID != "" {
		handoff, err := h.svc.MoveSessionToBackend(r.Context(), identity, sessionID,
			domain.BackendInstanceID(*body.BackendInstanceID))
		if err != nil {
			h.fail(w, err)
			return
		}
		// The handoff is answered on its own, because what it has to say — the
		// local skills the new backend does not have — belongs to nothing else.
		writeJSON(w, http.StatusOK, handoff)
		return
	}

	if body.WorkingDirectoryID != nil {
		var dirID *domain.KnownDirectoryID
		if *body.WorkingDirectoryID != nil && **body.WorkingDirectoryID != "" {
			value := domain.KnownDirectoryID(**body.WorkingDirectoryID)
			dirID = &value
		}
		session, err = h.svc.SetSessionWorkingDirectory(r.Context(), identity, sessionID, dirID)
		if err != nil {
			h.fail(w, err)
			return
		}
		changed = true
	}

	if !changed {
		snapshot, err := h.svc.SessionSnapshot(r.Context(), identity, sessionID, 1)
		if err != nil {
			h.fail(w, err)
			return
		}
		session = snapshot.Session
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *handler) archiveSession(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	h.setSessionStatus(w, r, identity, domain.SessionArchived)
}

func (h *handler) restoreSession(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	h.setSessionStatus(w, r, identity, domain.SessionActive)
}

func (h *handler) setSessionStatus(w http.ResponseWriter, r *http.Request, identity auth.Identity, status domain.SessionStatus) {
	session, err := h.svc.SetSessionStatus(r.Context(), identity,
		domain.SessionID(r.PathValue("sessionId")), status)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

// sessionHistory pages backwards through the timeline. Older history is loaded
// by scrolling; a client never replays the whole log.
func (h *handler) deleteSession(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.DeleteSession(r.Context(), identity, domain.SessionID(r.PathValue("sessionId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) sessionHistory(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	history, err := h.svc.SessionHistory(r.Context(), identity,
		domain.SessionID(r.PathValue("sessionId")),
		querySequence(r, "before"), queryInt(r, "limit", 100))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, history)
}

// postMessage appends a message, which becomes a new Job on the current Run and
// therefore resumes the same provider native session.
func (h *handler) postMessage(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Message string `json:"message"`
	}](w, r)
	if !ok {
		return
	}

	job, err := h.svc.PostMessage(r.Context(), identity,
		domain.SessionID(r.PathValue("sessionId")), body.Message,
		r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}
