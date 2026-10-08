package api_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/rclsilver/threavia/internal/backends/claude/runner"
)

// workingRunner stands in for Claude Code on a Job that is under way: it says
// the Job started, then works until released, and records every message that
// reaches it meanwhile.
type workingRunner struct {
	started  chan runner.StartParams
	injected chan injection
	release  chan struct{}
	once     sync.Once
}

type injection struct {
	jobID, text string
	interrupt   bool
}

func newWorkingRunner() *workingRunner {
	return &workingRunner{
		started:  make(chan runner.StartParams, 4),
		injected: make(chan injection, 4),
		release:  make(chan struct{}),
	}
}

func (r *workingRunner) Run(ctx context.Context, params runner.StartParams, sink runner.Sink) error {
	if err := sink.JobStarted(ctx, params.RunID, params.JobID); err != nil {
		return err
	}
	r.started <- params
	select {
	case <-r.release:
		return sink.JobCompleted(ctx, params.RunID, params.JobID, "done", nil)
	case <-ctx.Done():
		return nil
	}
}

func (r *workingRunner) Cancel(string) error { return runner.ErrUnknownJob }
func (r *workingRunner) Available() error    { return nil }

func (r *workingRunner) Inject(jobID, text string, interrupt bool) error {
	r.injected <- injection{jobID: jobID, text: text, interrupt: interrupt}
	return nil
}

func (r *workingRunner) stop() { r.once.Do(func() { close(r.release) }) }

// TestAMessageReachesTheRunningJob pins the deliveries of spec section 3.5 end
// to end: a message sent NEXT or NOW joins the Job already running instead of
// queueing a new one, reaches the provider the way it asked to, and is in that
// Job's timeline.
func TestAMessageReachesTheRunningJob(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	local := newWorkingRunner()
	t.Cleanup(local.stop)
	c.connectBackendWith(local, credential, t.TempDir())

	session := c.startSessionFrom(project, backendID, "web", "deploy the chart")
	first := receive(t, "the job to start", local.started)

	// The backend said what it can do, and a client can read it.
	var backends struct {
		Items []struct {
			ID       string   `json:"id"`
			Features []string `json:"features"`
		} `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/backends", nil, &backends, http.StatusOK)
	if len(backends.Items) != 1 || len(backends.Items[0].Features) != 2 {
		t.Fatalf("backends = %+v, want one announcing both deliveries", backends.Items)
	}

	var joined struct {
		ID string `json:"id"`
	}
	waitUntil(t, "the job to be running", func() bool {
		return c.do(http.MethodPost, "/api/v1/sessions/"+session+"/messages",
			map[string]any{"message": "also check the values", "delivery": "NEXT"}, &joined) == http.StatusOK
	})
	if joined.ID != first.JobID {
		t.Fatalf("the message joined job %s, want the running one %s", joined.ID, first.JobID)
	}
	next := receive(t, "the message to reach the provider", local.injected)
	if next.jobID != first.JobID || next.text != "also check the values" || next.interrupt {
		t.Fatalf("injected %+v, want the text, at the next step, for the running job", next)
	}

	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages",
		map[string]any{"message": "stop, use the staging values", "delivery": "NOW"}, &joined, http.StatusOK)
	now := receive(t, "the interruption to reach the provider", local.injected)
	if !now.interrupt || now.text != "stop, use the staging values" {
		t.Fatalf("injected %+v, want an interruption carrying the text", now)
	}

	// Both are part of the running Job's timeline, and no Job was queued.
	var snapshot struct {
		Jobs []struct {
			ID string `json:"id"`
		} `json:"jobs"`
		Events []struct {
			Type    string `json:"type"`
			JobID   string `json:"jobId"`
			Payload struct {
				Text     string `json:"text"`
				Delivery string `json:"delivery"`
			} `json:"payload"`
		} `json:"events"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &snapshot, http.StatusOK)
	if len(snapshot.Jobs) != 1 {
		t.Fatalf("%d jobs, want only the running one", len(snapshot.Jobs))
	}
	deliveries := map[string]string{}
	for _, event := range snapshot.Events {
		if event.Type == "user.message" && event.JobID == first.JobID {
			deliveries[event.Payload.Text] = event.Payload.Delivery
		}
	}
	if deliveries["also check the values"] != "NEXT" || deliveries["stop, use the staging values"] != "NOW" {
		t.Fatalf("messages of the running job = %v, want both with how they were delivered", deliveries)
	}

	// Once nothing runs, there is nothing to reach: the client is told to send
	// it as a message of its own rather than having it quietly queued.
	local.stop()
	waitUntil(t, "the job to end", func() bool {
		return c.do(http.MethodPost, "/api/v1/sessions/"+session+"/messages",
			map[string]any{"message": "and now?", "delivery": "NEXT"}, nil) == http.StatusConflict
	})
}
