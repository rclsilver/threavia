package api_test

import (
	"context"
	"net/http"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// TestUnreadSessionsStandOutUntilShown pins what makes a Session stand out in
// a list: something the agent said, or a Job that failed, while no client was
// showing it. The person's own messages never do. Reading on one client clears
// it for every client, and a mark that ran ahead of the timeline must not hide
// what the agent says next.
func TestUnreadSessionsStandOutUntilShown(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	session := c.startSession(projectID, c.backendID, dirID, "renew the certificates")
	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()

	unread := func() bool {
		t.Helper()
		var list struct {
			Items []struct {
				ID     string `json:"id"`
				Unread bool   `json:"unread"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/sessions", nil, &list, http.StatusOK)
		for _, item := range list.Items {
			if item.ID == session {
				return item.Unread
			}
		}
		t.Fatalf("session %s is not listed", session)
		return false
	}
	unreadInProject := func() int {
		t.Helper()
		var list struct {
			Items []struct {
				ID             string `json:"id"`
				UnreadSessions int    `json:"unreadSessions"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/projects", nil, &list, http.StatusOK)
		for _, item := range list.Items {
			if item.ID == projectID {
				return item.UnreadSessions
			}
		}
		t.Fatalf("project %s is not listed", projectID)
		return 0
	}
	lastSequence := func() int64 {
		t.Helper()
		events := c.snapshot(session).Events
		return events[len(events)-1].Sequence
	}
	read := func(sequence int64) {
		t.Helper()
		c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/read", map[string]any{"sequence": sequence}, nil, http.StatusNoContent)
	}
	say := func(text string) {
		t.Helper()
		backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
			return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), text)
		}))
	}

	// The person's own message is not news to them.
	if unread() || unreadInProject() != 0 {
		t.Fatal("a session holding only the person's message is unread")
	}

	say("Les certificats expirent dans 3 jours.")
	waitUntil(t, "the agent message to make the session unread", unread)
	if got := unreadInProject(); got != 1 {
		t.Fatalf("project unreadSessions = %d, want 1", got)
	}

	// Shown up to the last event: read, everywhere, and every client is told.
	read(lastSequence())
	if unread() || unreadInProject() != 0 {
		t.Fatal("the session is still unread once shown up to its last event")
	}
	if got := c.countEvents(session, "session.read"); got != 1 {
		t.Fatalf("%d session.read events, want 1", got)
	}

	// Reading again what was read says nothing to the other clients.
	read(lastSequence())
	if got := c.countEvents(session, "session.read"); got != 1 {
		t.Fatalf("reading again emitted another session.read: %d", got)
	}

	// A mark sent past the end of the timeline stops at its end, so what the
	// agent says next still stands out.
	read(lastSequence() + 1_000_000)
	say("Je les ai renouvelés.")
	waitUntil(t, "the next agent message to make the session unread again", unread)

	// A Job that failed is something to come back for too.
	read(lastSequence())
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobFailed(ctx, start.GetRunId(), start.GetJobId(), "CRASH", "the agent stopped", nil)
	}))
	waitUntil(t, "the failed job to make the session unread", unread)

	// An archived Session is out of the way: its Project does not point at it.
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/archive", nil, nil, http.StatusOK)
	if got := unreadInProject(); got != 0 {
		t.Fatalf("an archived unread session is counted on its project: %d", got)
	}

	if status := c.do(http.MethodPost, "/api/v1/sessions/"+session+"/read", map[string]any{"sequence": 0}, nil); status != http.StatusBadRequest {
		t.Fatalf("reading up to sequence 0 = %d, want 400", status)
	}
	alice := c.asUser("alice")
	if status := alice.do(http.MethodPost, "/api/v1/sessions/"+session+"/read", map[string]any{"sequence": lastSequence()}, nil); status != http.StatusNotFound {
		t.Fatalf("reading another user's session = %d, want 404", status)
	}
}
