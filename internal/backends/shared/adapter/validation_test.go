package adapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// endpointRunner registers its Job on the local tool endpoint with the Core
// Tools the adapter handed it, as the Claude runner does, and holds the Job
// open until the test ends.
type endpointRunner struct {
	tools    *mcp.Server
	endpoint chan string
	release  chan struct{}
}

func (r endpointRunner) Run(_ context.Context, params runner.StartParams, _ runner.Sink) error {
	r.endpoint <- r.tools.Register(params.JobID, "token-"+params.JobID, params.CoreTools)
	<-r.release
	return nil
}
func (endpointRunner) Cancel(string) error               { return nil }
func (endpointRunner) Inject(string, string, bool) error { return nil }
func (endpointRunner) Available() error                  { return nil }

// TestAToolThatWidensTheScopeIsAskedInEveryMode is the guard against an agent
// granting itself the user's home: working_directory_set decides where the next
// run starts, so it raises a ValidationRequest even in AUTONOMOUS, where the
// policy would allow any tool it does not know, and the user's answer is what
// the provider hears.
func TestAToolThatWidensTheScopeIsAskedInEveryMode(t *testing.T) {
	for _, mode := range []backendv1.ExecutionMode{
		backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
	} {
		for _, approved := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s approved=%t", mode, approved), func(t *testing.T) {
				a, store, endpoint := runningJob(t, mode)
				ctx := context.Background()

				answered := make(chan string, 1)
				go func() {
					answered <- approvalPrompt(endpoint, "mcp__threavia__working_directory_set",
						map[string]any{"knownDirectoryId": "kd-home"})
				}()

				request := awaitValidation(t, store)
				if request.GetTitle() != "working_directory_set: kd-home" {
					t.Fatalf("title = %q, want it to name the directory being granted", request.GetTitle())
				}
				if err := a.OnValidationResolution(ctx, &backendv1.ValidationResolution{
					RunId: "run-1", JobId: "job-1", RequestId: request.GetRequestId(), Approved: approved,
				}); err != nil {
					t.Fatalf("resolving the validation: %v", err)
				}

				want := `"behavior":"deny"`
				if approved {
					want = `"behavior":"allow"`
				}
				select {
				case got := <-answered:
					if !strings.Contains(got, want) {
						t.Fatalf("approved=%t answered %s, want %s", approved, got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the provider never heard the answer")
				}
			})
		}
	}
}

// runningJob starts job-1 under the given mode, with a Core Tool that requires
// validation among those Core declared, and returns its tool endpoint.
func runningJob(t *testing.T, mode backendv1.ExecutionMode) (*adapter.Adapter, state.Store, string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tools := mcp.New(logger)
	r := endpointRunner{tools: tools, endpoint: make(chan string, 1), release: make(chan struct{})}
	t.Cleanup(func() { close(r.release) })

	store := state.NewMemoryStore()
	a := adapter.New(adapter.Config{}, r, store, logger)
	tools.SetAsker(a)
	if err := tools.Start(); err != nil {
		t.Fatalf("starting the tool endpoint: %v", err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })

	cfg := client.DefaultConfig()
	cfg.CoreAddress = "core.invalid:9090"
	cfg.Token = "credential"
	cfg.InstanceName = "laptop"
	sdk, err := client.New(cfg, a, store, logger)
	if err != nil {
		t.Fatalf("building the sdk client: %v", err)
	}
	a.Bind(sdk)

	if err := a.OnStartJob(context.Background(), &backendv1.StartJob{
		RunId: "run-1", JobId: "job-1", Prompt: "travaille dans mon dossier personnel",
		ProjectContext: &backendv1.ProjectContext{
			WorkingDirectoryPath: t.TempDir(),
			Tools: []*backendv1.CoreToolSpec{
				{Name: "task_create"},
				{Name: "working_directory_set", RequiresValidation: true},
			},
		},
		ExecutionPolicy: &backendv1.ExecutionPolicy{Mode: mode},
	}); err != nil {
		t.Fatalf("starting the job: %v", err)
	}

	select {
	case endpoint := <-r.endpoint:
		return a, store, endpoint
	case <-time.After(5 * time.Second):
		t.Fatal("the job never started")
		return nil, nil, ""
	}
}

// approvalPrompt asks the endpoint what Claude Code asks before running a tool,
// and returns the decision text, or what went wrong in its place.
func approvalPrompt(endpoint, tool string, input map[string]any) string {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      mcp.ToolApprovalPrompt,
			"arguments": map[string]any{"tool_name": tool, "input": input},
		},
	})
	if err != nil {
		return err.Error()
	}
	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err.Error()
	}
	if len(decoded.Result.Content) == 0 {
		return "no content"
	}
	return decoded.Result.Content[0].Text
}

// awaitValidation waits for the ValidationRequest the adapter buffered for Core.
func awaitValidation(t *testing.T, store state.Store) *backendv1.ValidationRequested {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := store.PendingEvents(context.Background())
		if err != nil {
			t.Fatalf("reading the buffered events: %v", err)
		}
		for _, event := range pending {
			if request := event.GetValidationRequested(); request != nil {
				return request
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no validation was requested")
	return nil
}
