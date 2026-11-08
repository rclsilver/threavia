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
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+started.Session.ID+"/messages", map[string]any{"message": "next", "delivery": "NEXT", "artifactIds": []string{file.ID}}, nil, http.StatusBadRequest)

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

// A running step takes text only, so files sent to interrupt replace the Job,
// even one waiting for an answer: the agent stops waiting and reads them next,
// in the same conversation.
func TestInterruptingWithFilesReplacesAJobWaitingForAnAnswer(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.svc.SetObjectStore(newMemoryObjects(), 4<<20)
	var file artifactResponse
	c.mustUpload("/api/v1/projects/"+projectID+"/artifacts?filename=screen.png", "image/png", "image bytes", &file)
	session := c.startSession(projectID, c.backendID, dirID, "Ask me")
	start := receive(t, "the first job", backend.starts)
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(), &backendv1.UserInputRequested{RequestId: "what", Prompt: "What do you see?"})
	}))
	waitUntil(t, "the question to wait", func() bool { return len(c.snapshot(session).Attention.UserInputs) == 1 })

	path := "/api/v1/sessions/" + session + "/messages"
	c.mustDo(http.MethodPost, path, map[string]any{"message": "this", "delivery": "NEXT", "artifactIds": []string{file.ID}}, nil, http.StatusBadRequest)
	var replaced, replayed struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	for _, target := range []any{&replaced, &replayed} {
		c.mustDo(http.MethodPost, path, map[string]any{"message": "this", "delivery": "NOW", "artifactIds": []string{file.ID}}, target, http.StatusOK, [2]string{"Idempotency-Key", "interrupt-with-files"})
	}
	if replaced.ID == "" || replaced.ID == start.GetJobId() || replaced.Status != "QUEUED" || replayed.ID != replaced.ID {
		t.Fatalf("replacement = %+v, replay = %+v, want one queued job of its own", replaced, replayed)
	}
	if receive(t, "the waiting job to be stopped", backend.cancels).GetJobId() != start.GetJobId() {
		t.Fatal("the cancel went to another job")
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCancelled(ctx, start.GetRunId(), start.GetJobId(), nil)
	}))
	next := receive(t, "the files to be sent in its place", backend.starts)
	if next.GetJobId() != replaced.ID || next.GetRunId() != start.GetRunId() || len(next.Attachments) != 1 || string(next.Attachments[0].Content) != "image bytes" {
		t.Fatalf("next job = %v, want the files on the same run", next)
	}
	if len(c.snapshot(session).Attention.UserInputs) != 0 {
		t.Fatal("the question of the stopped job must no longer wait")
	}
}
