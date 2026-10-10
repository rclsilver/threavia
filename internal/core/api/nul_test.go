package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/backends/claude/runner"
)

// binaryOutputRunner stands in for Claude Code on a Job whose tool read a
// binary file: the output carries NUL characters, as `cat` of one does.
type binaryOutputRunner struct{}

func (binaryOutputRunner) Run(ctx context.Context, params runner.StartParams, sink runner.Sink) error {
	if err := sink.JobStarted(ctx, params.RunID, params.JobID); err != nil {
		return err
	}
	input := map[string]any{"command": "cat firmware.bin"}
	if err := sink.ToolStarted(ctx, params.RunID, params.JobID, "call-1", "Bash", input); err != nil {
		return err
	}
	output := map[string]any{"stdout": "ELF\x00\x01\x02header\x00end", "path\x00name": "kept"}
	if err := sink.ToolCompleted(ctx, params.RunID, params.JobID, "call-1", "Bash", output); err != nil {
		return err
	}
	return sink.JobCompleted(ctx, params.RunID, params.JobID, "read it", nil)
}

func (binaryOutputRunner) Cancel(string) error               { return runner.ErrUnknownJob }
func (binaryOutputRunner) Inject(string, string, bool) error { return runner.ErrUnknownJob }
func (binaryOutputRunner) Available() error                  { return nil }

// TestAToolOutputWithNULIsKept pins that an event carrying a NUL character
// reaches the timeline. PostgreSQL refuses \u0000 in jsonb, and such an event
// used to be dropped: the insert failed, and the backend kept replaying it.
func TestAToolOutputWithNULIsKept(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	c.connectBackendWith(binaryOutputRunner{}, credential, t.TempDir())
	session := c.startSessionFrom(project, backendID, "web", "read the firmware")

	var completed struct {
		Payload map[string]any `json:"payload"`
	}
	waitUntil(t, "the tool output in the timeline", func() bool {
		var history struct {
			Items []struct {
				Type    string         `json:"type"`
				Payload map[string]any `json:"payload"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/events", nil, &history, http.StatusOK)
		for _, event := range history.Items {
			if event.Type == "tool.completed" {
				completed.Payload = event.Payload
				return true
			}
		}
		return false
	})

	if strings.Contains(c.lastBody, `\u0000`) {
		t.Fatalf("the timeline still carries a NUL: %s", c.lastBody)
	}
	// The text is kept, every NUL shown as the replacement character, in values
	// and in keys alike; the other control characters are left as they were.
	output, _ := completed.Payload["output"].(map[string]any)
	if got := output["stdout"]; got != "ELF�\x01\x02header�end" {
		t.Fatalf("stdout = %q, want the NULs replaced and the rest kept", got)
	}
	if got := output["path�name"]; got != "kept" {
		t.Fatalf("output = %v, want the key with its NUL replaced", output)
	}
}
