//go:build kubernetes_integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/monitors"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/skills"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
	"github.com/rclsilver/threavia/internal/execution/client"
	"github.com/rclsilver/threavia/internal/execution/remote"
	"github.com/rclsilver/threavia/pkg/monitor"
)

type sink struct {
	mu                       sync.Mutex
	completed                bool
	failure, native, message string
}

func (s *sink) JobStarted(context.Context, string, string) error { return nil }
func (s *sink) NativeSessionBound(_ context.Context, _, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.native = id
	return nil
}
func (s *sink) AgentMessage(_ context.Context, _, _, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message += text
	return nil
}
func (s *sink) ToolStarted(context.Context, string, string, string, string, map[string]any) error {
	return nil
}
func (s *sink) ToolCompleted(context.Context, string, string, string, string, map[string]any) error {
	return nil
}
func (s *sink) ToolFailed(context.Context, string, string, string, string, string) error  { return nil }
func (s *sink) WorkspaceChanged(context.Context, string, string, workspace.Summary) error { return nil }
func (s *sink) JobCompleted(context.Context, string, string, string, *backendv1.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = true
	return nil
}
func (s *sink) JobFailed(_ context.Context, _, _, code, message string, _ *backendv1.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = code + ": " + message
	return nil
}
func (s *sink) JobCancelled(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = "cancelled"
	return nil
}
func (s *sink) Progress(context.Context, string, string, string, string) {}

func TestClusterJobsAndMonitors(t *testing.T) {
	endpoint := os.Getenv("EXECUTION_TEST_ENDPOINT")
	if endpoint == "" {
		t.Fatal("this suite must run inside the isolated integration cluster")
	}
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			c, err := client.New(ctx, client.Config{Endpoint: endpoint, CAFile: "/executor-ca/ca.crt", TokenFile: "/executor-clients/" + provider + "/token", WorkingDirectory: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := remote.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
			catalog, err := r.Models(ctx)
			if err != nil || catalog == nil || len(catalog.Models) == 0 {
				t.Fatalf("provider probe: %v", err)
			}
			pol := policy.Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_GUARDED}
			run := uuid.NewString()
			pol.Scratch = filepath.Join("/shared/scratch", run)
			params := runner.StartParams{RunID: run, JobID: uuid.NewString(), WorkingDirectory: "/workspace", Prompt: "fixture", Policy: pol}
			bundle := filepath.Join("/shared/skills/bundles", provider, "fixture")
			if err = os.MkdirAll(bundle, 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(bundle, "SKILL.md"), []byte("---\nname: fixture\ndescription: Fixture skill\n---\nTest skill."), 0644); err != nil {
				t.Fatal(err)
			}
			prepareSkills := func() {
				t.Helper()
				params.SkillDirectory, err = skills.Assemble(filepath.Join("/shared/skills/jobs", params.JobID), []skills.Entry{{Name: "fixture", Path: bundle}})
				if err != nil {
					t.Fatal(err)
				}
			}
			prepareSkills()
			first := &sink{}
			if err = r.Run(ctx, params, first); err != nil {
				t.Fatal(err)
			}
			first.mu.Lock()
			completed, failure, native, message := first.completed, first.failure, first.native, first.message
			first.mu.Unlock()
			if !completed || failure != "" || native == "" || !strings.Contains(message, "fixture completed") {
				t.Fatalf("first Job: completed=%v native=%q failure=%s message=%s", completed, native, failure, message)
			}
			params.JobID = uuid.NewString()
			prepareSkills()
			params.NativeSessionID = native
			second := &sink{}
			if err = r.Run(ctx, params, second); err != nil {
				t.Fatal(err)
			}
			second.mu.Lock()
			completed, failure = second.completed, second.failure
			second.mu.Unlock()
			if !completed || failure != "" {
				t.Fatalf("resumed Job: completed=%v failure=%s", completed, failure)
			}
			params.JobID = uuid.NewString()
			prepareSkills()
			params.Policy.AllowFilesystemWrite = true
			params.Prompt = "EXPECT_WORKSPACE_WRITE"
			third := &sink{}
			if err = r.Run(ctx, params, third); err != nil {
				t.Fatal(err)
			}
			third.mu.Lock()
			completed, failure, message = third.completed, third.failure, third.message
			third.mu.Unlock()
			if !completed || failure != "" || !strings.Contains(message, "fixture completed") {
				t.Fatalf("writable Job: completed=%v failure=%s message=%s", completed, failure, message)
			}
			m := monitors.NewWithExecutor(filepath.Join(t.TempDir(), "monitors.json"), c)
			id := uuid.NewString()
			watch := monitor.Snapshot{ID: id, RunID: run, JobID: params.JobID, Command: "printf monitor-alive; sleep 120", Description: "integration monitor", StartedAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Minute)}
			if err = m.Start(watch, "/workspace", false, false); err != nil {
				t.Fatal(err)
			}
			waitMonitor(t, m, id, "RUNNING", "monitor-alive")
			// The originating provider Job already ended; the Monitor is still alive.
			if err = m.Stop(id, run); err != nil {
				t.Fatal(err)
			}
			waitMonitor(t, m, id, "STOPPED", "monitor-alive")
			for _, allow := range []bool{false, true} {
				id = uuid.NewString()
				watch.ID = id
				watch.ExpiresAt = time.Now().Add(time.Minute)
				// Connectivity to the in-cluster echo server is known reachable with
				// approved networking. The offline helper must refuse even TCP/443.
				watch.Command = "if bash -c 'exec 3<>/dev/tcp/echo/8080'; then printf connected; else printf denied; fi"
				if err = m.Start(watch, "/workspace", false, allow); err != nil {
					t.Fatal(err)
				}
				want := "denied"
				if allow {
					want = "connected"
				}
				waitMonitor(t, m, id, "COMPLETED", want)
			}
			// A child deliberately escapes the shell process group. Container
			// termination, rather than a PID/group kill, must stop its writes.
			id = uuid.NewString()
			watch.ID = id
			watch.ExpiresAt = time.Now().Add(time.Minute)
			marker := "/workspace/project/heartbeat-" + id
			watch.Command = "/usr/local/bin/fixture-provider --spawn-detached-heartbeat " + marker
			if err = m.Start(watch, "/workspace/project", true, false); err != nil {
				t.Fatal(err)
			}
			waitMonitor(t, m, id, "FAILED", "detached child started")
			before, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			after, err := os.ReadFile(marker)
			if err != nil || string(before) != string(after) {
				t.Fatal("detached child was still running after container stop confirmation")
			}
			// A new control backend incarnation fences the first one's Monitor
			// before it can accept new work with the same execution profile.
			id = uuid.NewString()
			watch.ID = id
			watch.Command = "printf takeover-watch; sleep 120"
			watch.ExpiresAt = time.Now().Add(time.Minute)
			if err = m.Start(watch, "/workspace", false, false); err != nil {
				t.Fatal(err)
			}
			waitMonitor(t, m, id, "RUNNING", "takeover-watch")
			next, err := client.New(ctx, client.Config{Endpoint: endpoint, CAFile: "/executor-ca/ca.crt", TokenFile: "/executor-clients/" + provider + "/token", WorkingDirectory: "/workspace"})
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			waitMonitor(t, m, id, "FAILED", "takeover-watch")
			if _, err = next.Probe(ctx); err != nil {
				t.Fatal("replacement backend could not probe after fencing:", err)
			}
		})
	}
}
func waitMonitor(t *testing.T, m *monitors.Manager, id, state, output string) {
	t.Helper()
	limit := time.Now().Add(45 * time.Second)
	for time.Now().Before(limit) {
		values, err := m.Snapshots()
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range values {
			if v.ID == id {
				if v.State == state && strings.Contains(v.Output, output) {
					return
				}
				if v.State == "FAILED" {
					t.Fatalf("monitor failed: %s (%s)", v.Error, v.Output)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	values, _ := m.Snapshots()
	t.Fatal(fmt.Sprintf("monitor %s did not reach %s/%q: %+v", id, state, output, values))
}
