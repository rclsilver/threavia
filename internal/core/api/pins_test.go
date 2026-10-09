package api_test

import (
	"net/http"
	"testing"
)

// TestPinnedSessionsAreReachedFromAnyProject pins the shortcut a person makes
// to a Session: listed from every Project in the order it was pinned, gone
// when unpinned, out of the way while archived, and nobody else's to pin.
func TestPinnedSessionsAreReachedFromAnyProject(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	homelab := c.createProject("homelab")
	website := c.createProject("website")
	backendID, credential := c.registerBackend("laptop")
	c.connectBackend(credential)

	certs := c.startSession(homelab, backendID, "", "renew the certificates")
	post := c.startSession(website, backendID, "", "write the post")
	other := c.startSession(homelab, backendID, "", "something else")

	pinned := func() []string {
		t.Helper()
		var list struct {
			Items []struct {
				ID       string `json:"id"`
				PinnedAt string `json:"pinnedAt"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/me/pinned-sessions", nil, &list, http.StatusOK)
		ids := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			if item.PinnedAt == "" {
				t.Fatalf("pinned session %s carries no pinnedAt", item.ID)
			}
			ids = append(ids, item.ID)
		}
		return ids
	}
	pin := func(session string, value bool) {
		t.Helper()
		c.mustDo(http.MethodPatch, "/api/v1/sessions/"+session, map[string]any{"pinned": value}, nil, http.StatusOK)
	}

	if got := pinned(); len(got) != 0 {
		t.Fatalf("nothing pinned yet, got %v", got)
	}

	// Across Projects, in the order they were pinned; pinning again keeps the
	// place.
	pin(post, true)
	pin(certs, true)
	pin(post, true)
	if got := pinned(); len(got) != 2 || got[0] != post || got[1] != certs {
		t.Fatalf("pinned = %v, want [%s %s]", got, post, certs)
	}

	// An archived Session is out of the way, and back where it was once
	// restored.
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+post+"/archive", nil, nil, http.StatusOK)
	if got := pinned(); len(got) != 1 || got[0] != certs {
		t.Fatalf("with one archived, pinned = %v, want [%s]", got, certs)
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+post+"/restore", nil, nil, http.StatusOK)
	if got := pinned(); len(got) != 2 || got[0] != post {
		t.Fatalf("restored, pinned = %v, want %s first again", got, post)
	}

	pin(post, false)
	if got := pinned(); len(got) != 1 || got[0] != certs {
		t.Fatalf("unpinned, pinned = %v, want [%s]", got, certs)
	}

	// Someone else's Session is not found, and theirs are not listed here.
	alice := c.asUser("alice")
	if status := alice.do(http.MethodPatch, "/api/v1/sessions/"+other, map[string]any{"pinned": true}, nil); status != http.StatusNotFound {
		t.Fatalf("pinning another user's session = %d, want 404", status)
	}
	var theirs struct {
		Items []struct{} `json:"items"`
	}
	alice.mustDo(http.MethodGet, "/api/v1/me/pinned-sessions", nil, &theirs, http.StatusOK)
	if len(theirs.Items) != 0 {
		t.Fatalf("another user sees %d pinned sessions, want none", len(theirs.Items))
	}
}
