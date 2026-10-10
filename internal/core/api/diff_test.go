package api_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
)

// editingRunner stands in for Claude Code on a Job that edits one file of a
// real repository, and reports the change the way the real runner does.
type editingRunner struct {
	repository string
}

func (r editingRunner) Run(ctx context.Context, params runner.StartParams, sink runner.Sink) error {
	if err := sink.JobStarted(ctx, params.RunID, params.JobID); err != nil {
		return err
	}
	before := workspace.Observe(ctx, r.repository)
	if err := os.WriteFile(filepath.Join(r.repository, "values.yaml"), []byte("replicas: 3\n"), 0o644); err != nil {
		return err
	}
	if err := sink.WorkspaceChanged(ctx, params.RunID, params.JobID, workspace.Since(ctx, r.repository, before)); err != nil {
		return err
	}
	return sink.JobCompleted(ctx, params.RunID, params.JobID, "scaled", nil)
}

func (editingRunner) Cancel(string) error               { return runner.ErrUnknownJob }
func (editingRunner) Inject(string, string, bool) error { return runner.ErrUnknownJob }
func (editingRunner) Available() error                  { return nil }

// TestTheDiffOfAChangeIsFetchedFromTheBackend pins spec section 22 end to end:
// the change summary names files, and the diff of one is computed by the
// backend that holds the working directory when someone asks for it.
func TestTheDiffOfAChangeIsFetchedFromTheBackend(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "values.yaml"), []byte("replicas: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch", "main"},
		{"add", "values.yaml"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repository
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	c.connectBackendWith(editingRunner{repository: repository}, credential, t.TempDir())
	session := c.startSessionFrom(project, backendID, "web", "scale it")

	var change struct {
		Sequence int64 `json:"sequence"`
	}
	waitUntil(t, "the change summary", func() bool {
		var history struct {
			Items []struct {
				Sequence int64  `json:"sequence"`
				Type     string `json:"type"`
			} `json:"items"`
		}
		c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/events", nil, &history, http.StatusOK)
		for _, event := range history.Items {
			if event.Type == "workspace.changed" {
				change.Sequence = event.Sequence
				return true
			}
		}
		return false
	})

	diffOf := func(path string, target any) int {
		return c.do(http.MethodGet, "/api/v1/sessions/"+session+"/diff?event="+
			strconv.FormatInt(change.Sequence, 10)+"&path="+url.QueryEscape(path), nil, target)
	}

	var diff struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	}
	if status := diffOf("values.yaml", &diff); status != http.StatusOK {
		t.Fatalf("GET diff = %d: %s", status, c.lastBody)
	}
	if !strings.Contains(diff.Diff, "+replicas: 3") {
		t.Fatalf("diff = %q, want the change the job made", diff.Diff)
	}

	// Only what the summary listed: it is not a way to read the repository.
	if status := diffOf("../../etc/passwd", nil); status != http.StatusNotFound {
		t.Fatalf("a file outside the change = %d, want 404", status)
	}
}
