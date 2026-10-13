package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestPromptAttachmentsReachBackendAndDurableTimeline(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.svc.SetObjectStore(newMemoryObjects(), 4<<20)
	var file artifactResponse
	c.mustUpload("/api/v1/projects/"+projectID+"/artifacts?filename=screen.png", "image/png", "image bytes", &file)
	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{"projectId": projectID, "backendInstanceId": c.backendID, "workingDirectoryId": dirID, "message": "Describe this", "artifactIds": []string{file.ID}}, &started, http.StatusCreated)
	start := receive(t, "attached image", backend.starts)
	if len(start.Attachments) != 1 || string(start.Attachments[0].Content) != "image bytes" || start.Attachments[0].ArtifactId != file.ID {
		t.Fatalf("attachments = %v", start.Attachments)
	}
	var snapshot snapshotResponse
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+started.Session.ID, nil, &snapshot, http.StatusOK)
	found := false
	for _, event := range snapshot.Events {
		if event.Type != "user.message" {
			continue
		}
		var payload struct {
			Attachments []struct {
				ArtifactID string `json:"artifactId"`
				Filename   string
			}
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		found = len(payload.Attachments) == 1 && payload.Attachments[0].ArtifactID == file.ID && payload.Attachments[0].Filename == "screen.png"
	}
	if !found {
		t.Fatal("timeline lost attachment metadata")
	}

	// Existing conversations may send only files; replay must not enqueue twice.
	var queued, replayed idOnly
	for _, target := range []*idOnly{&queued, &replayed} {
		c.mustDo(http.MethodPost, "/api/v1/sessions/"+started.Session.ID+"/messages", map[string]any{"message": "", "artifactIds": []string{file.ID}}, target, http.StatusCreated, [2]string{"Idempotency-Key", "attachment-retry"})
	}
	if queued.ID != replayed.ID {
		t.Fatal("attachment replay queued twice")
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+started.Session.ID+"/messages", map[string]any{"message": "now", "delivery": "NOW", "artifactIds": []string{file.ID}}, nil, http.StatusBadRequest)

	other := c.createProject("other")
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{"projectId": other, "backendInstanceId": c.backendID, "message": "x", "artifactIds": []string{file.ID}}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+started.Session.ID+"/messages", map[string]any{"message": "x", "artifactIds": []string{file.ID, file.ID}}, nil, http.StatusBadRequest)
	var large artifactResponse
	c.mustUpload("/api/v1/projects/"+projectID+"/artifacts?filename=large.wav", "audio/wav", strings.Repeat("x", (3<<20)+1), &large)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+started.Session.ID+"/messages", map[string]any{"message": "x", "artifactIds": []string{large.ID}}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodDelete, "/api/v1/artifacts/"+file.ID, nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+started.Session.ID+"/messages", map[string]any{"message": "", "artifactIds": []string{file.ID}}, &replayed, http.StatusCreated, [2]string{"Idempotency-Key", "attachment-retry"})
	if queued.ID != replayed.ID {
		t.Fatal("deleted attachment broke replay")
	}
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.RunId, start.JobId, "done", nil)
	}))
	waitUntil(t, "deleted attachment to fail queued work", func() bool { return c.jobStatus(started.Session.ID, queued.ID) == "FAILED" })
}
