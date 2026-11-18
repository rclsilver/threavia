package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// An agent raises notifications for the person without waiting: they are
// clipped, an identical unread one is counted rather than repeated, WARNING
// comes first and reading is idempotent.
func TestNotifyToolRaisesCountsAndClears(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Watch")
	start := receive(t, "the job", backend.starts)
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	fail := func(name string, input map[string]any) {
		backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}

	fail("notify", map[string]any{"level": "ERROR", "title": "t", "message": "m"})
	fail("notify", map[string]any{"level": "INFO", "title": " ", "message": "m"})
	fail("notify", map[string]any{"level": "INFO", "title": "t", "message": ""})

	long := call("notify", map[string]any{"level": "INFO", "title": strings.Repeat("é", 300), "message": strings.Repeat("m", 3000)})["notification"].(map[string]any)
	if got := len([]rune(long["title"].(string))); got != 120 {
		t.Fatalf("title = %d characters, want 120", got)
	}
	if got := len(long["message"].(string)); got != 2000 {
		t.Fatalf("message = %d characters, want 2000", got)
	}

	for i := 0; i < 3; i++ {
		call("notify", map[string]any{"level": "WARNING", "title": "Disk", "message": "90% full"})
	}
	list := call("notification_list", map[string]any{})["notifications"].([]any)
	if len(list) != 2 {
		t.Fatalf("notification_list = %v, want the warning and the info", list)
	}
	first := list[0].(map[string]any)
	if first["level"] != "WARNING" || first["count"] != float64(3) {
		t.Fatalf("first = %v, want the WARNING counted 3 times", first)
	}

	call("notification_mark_read", map[string]any{"notificationId": first["id"]})
	call("notification_mark_read", map[string]any{"notificationId": first["id"]})
	if left := call("notification_list", map[string]any{})["notifications"].([]any); len(left) != 1 {
		t.Fatalf("after reading one: %v", left)
	}
	fail("notification_mark_read", map[string]any{})
	call("notification_mark_read", map[string]any{"sessionId": first["scope"].(map[string]any)["sessionId"]})
	if left := call("notification_list", map[string]any{})["notifications"].([]any); len(left) != 0 {
		t.Fatalf("after reading the session: %v", left)
	}
	if all := call("notification_list", map[string]any{"unread": false})["notifications"].([]any); len(all) != 2 {
		t.Fatalf("read notifications are listed with unread=false: %v", all)
	}
}

func TestNotificationEndpoints(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Watch")
	start := receive(t, "the job", backend.starts)
	ctx := context.Background()
	notify := func(level, title string) {
		backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), "notify", map[string]any{"level": level, "title": title, "message": "details"})
	}
	notify("INFO", "fyi")
	notify("WARNING", "look")

	var unread struct {
		Items []struct {
			ID    string `json:"id"`
			Level string `json:"level"`
		} `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/notifications?unread=true", nil, &unread, http.StatusOK)
	if len(unread.Items) != 2 || unread.Items[0].Level != "WARNING" {
		t.Fatalf("unread = %+v, want the WARNING first", unread.Items)
	}

	c.mustDo(http.MethodPost, "/api/v1/notifications/"+unread.Items[0].ID+"/read", nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodPost, "/api/v1/notifications/"+unread.Items[0].ID+"/read", nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodPost, "/api/v1/notifications/"+domain.NewUUID()+"/read", nil, nil, http.StatusNotFound)
	c.mustDo(http.MethodGet, "/api/v1/me/notifications?unread=true", nil, &unread, http.StatusOK)
	if len(unread.Items) != 1 || unread.Items[0].Level != "INFO" {
		t.Fatalf("after one read: %+v", unread.Items)
	}
	c.mustDo(http.MethodPost, "/api/v1/notifications/read", nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodGet, "/api/v1/me/notifications?unread=true", nil, &unread, http.StatusOK)
	if len(unread.Items) != 0 {
		t.Fatalf("after read all: %+v", unread.Items)
	}
}
