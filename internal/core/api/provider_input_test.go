package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestProviderRecoveryDoesNotDuplicateDeliveredAttachments(t *testing.T) {
	for _, prompt := range []string{"Original image request", ""} {
		t.Run(prompt, func(t *testing.T) {
			c, backend, project, dir := setup(t)
			c.svc.SetObjectStore(newMemoryObjects(), 4<<20)
			var file artifactResponse
			c.mustUpload("/api/v1/projects/"+project+"/artifacts?filename=screen.png", "image/png", "image bytes", &file)
			var created startSessionResponse
			c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{"projectId": project, "backendInstanceId": c.backendID, "workingDirectoryId": dir, "message": prompt, "artifactIds": []string{file.ID}}, &created, http.StatusCreated)
			start := receive(t, "initial attached input", backend.starts)
			ctx := context.Background()
			bind := func(native string) {
				event, err := backend.events.NativeSessionBound(ctx, start.RunId, start.JobId, native)
				if err != nil {
					t.Fatal(err)
				}
				backend.emit(t, ctx, event)
			}
			retry := func(receipt string) *backendv1.StartJob {
				event, err := backend.events.JobWaitingBackend(ctx, start.RunId, start.JobId, &backendv1.JobWaitingBackend{
					Reason: "QUOTA_EXCEEDED", Message: "Quota exhausted", RetryAt: timestamppb.New(time.Now().Add(time.Hour)), InputReceipt: receipt,
				})
				if err != nil {
					t.Fatal(err)
				}
				backend.emit(t, ctx, event)
				waitUntil(t, "stopped attempt", func() bool { return c.jobStatus(created.Session.ID, start.JobId) == "WAITING_BACKEND" })
				providerDue(t, c, start.JobId)
				c.svc.RecoverProviderJobs(ctx)
				return receive(t, "resumed job", backend.starts)
			}
			bind("native-one")
			// A turn/binding is not proof of delivery. Old backends and early
			// auth/quota refusals must still deliver the entire original input.
			again := retry("")
			if len(again.Attachments) != 1 || !strings.Contains(again.Prompt, prompt) {
				t.Fatal("input lost before provider acknowledgement", again)
			}
			for attempt := 0; attempt < 4; attempt++ {
				receipt := ""
				if attempt == 0 {
					receipt = resume.InputReceipt(start.JobId, "native-one")
				}
				// Subsequent waits deliberately omit the receipt: Core must
				// retain the earlier acknowledgement from durable history.
				again = retry(receipt)
				if again.JobId != start.JobId || again.NativeSessionId != "native-one" || len(again.Attachments) != 0 || (prompt != "" && strings.Contains(again.Prompt, prompt)) {
					t.Fatalf("attempt %d duplicated original input: %+v", attempt, again)
				}
			}
			bind("native-two")
			again = retry("")
			if len(again.Attachments) != 1 || (prompt != "" && !strings.Contains(again.Prompt, prompt)) {
				t.Fatal("receipt for another conversation discarded input", again)
			}
			done, _ := backend.events.JobCompleted(ctx, start.RunId, start.JobId, "done", nil)
			backend.emit(t, ctx, done)
			waitUntil(t, "completion", func() bool { return c.jobStatus(created.Session.ID, start.JobId) == "COMPLETED" })
			c.mustDo(http.MethodPost, "/api/v1/sessions/"+created.Session.ID+"/messages", map[string]any{"message": "New image request", "artifactIds": []string{file.ID}}, nil, http.StatusCreated)
			next := receive(t, "new job with the same image", backend.starts)
			if next.JobId == start.JobId || len(next.Attachments) != 1 || next.Prompt != "New image request" {
				t.Fatal("a new request was deduplicated", next)
			}
		})
	}
}
