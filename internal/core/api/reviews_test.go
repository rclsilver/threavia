package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

type reviewCaller interface {
	CallCoreTool(context.Context, string, string, map[string]any, string) (map[string]any, error)
}
type reviewEditor struct{ repos []string }

func (r reviewEditor) Run(ctx context.Context, p runner.StartParams, s runner.Sink) error {
	if err := s.JobStarted(ctx, p.RunID, p.JobID); err != nil {
		return err
	}
	caller := s.(reviewCaller)
	for _, repo := range r.repos {
		if _, err := caller.CallCoreTool(ctx, p.JobID, "review_repository_register", map[string]any{"directory": repo}, ""); err != nil {
			return s.JobFailed(ctx, p.RunID, p.JobID, "REVIEW", err.Error(), nil)
		}
	}
	value := "replicas: 2\n"
	if strings.Contains(p.Prompt, "Address this human code review") {
		value = "replicas: 3\n"
	}
	for _, repo := range r.repos {
		if err := os.WriteFile(filepath.Join(repo, "values.yaml"), []byte(value), 0600); err != nil {
			return err
		}
	}
	return s.JobCompleted(ctx, p.RunID, p.JobID, "Updated configuration", nil)
}
func (reviewEditor) Cancel(string) error               { return runner.ErrUnknownJob }
func (reviewEditor) Inject(string, string, bool) error { return runner.ErrUnknownJob }
func (reviewEditor) Available() error                  { return nil }

func reviewRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "values.yaml"), []byte("replicas: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--initial-branch", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, output)
		}
	}
	return repo
}

func TestHumanReviewMultiRepoDraftPublicationAndApproval(t *testing.T) {
	t.Parallel()
	repos := []string{reviewRepository(t), reviewRepository(t)}
	c := newCore(t)
	project := c.createProject("review")
	backend, credential := c.registerBackend("review backend")
	c.connectBackendWith(reviewEditor{repos: repos}, credential, t.TempDir())
	session := c.startSession(project, backend, "", "Make the changes")
	waitUntil(t, "initial work completed", func() bool {
		snap := c.snapshot(session)
		return len(snap.Jobs) == 1 && snap.Jobs[0].Status == "COMPLETED"
	})
	path := "/api/v1/sessions/" + session + "/review"
	var review domain.Review
	read := func() { c.mustDo(http.MethodGet, path, nil, &review, http.StatusOK) }
	act := func(in service.ReviewAction) {
		for attempt := 0; attempt < 3; attempt++ {
			in.ExpectedVersion = review.Version
			status := c.do(http.MethodPost, path, in, &review)
			if status == http.StatusOK {
				return
			}
			if status != http.StatusConflict || !strings.Contains(c.lastBody, "review changed") {
				t.Fatalf("%s failed: HTTP %d %s", in.Action, status, c.lastBody)
			}
			read()
		}
		t.Fatalf("%s kept conflicting with background captures", in.Action)
	}
	read()
	if len(review.Repositories) != 2 {
		t.Fatalf("repositories: %+v", review.Repositories)
	}
	act(service.ReviewAction{Action: "require"})
	if !review.Required || len(review.Revisions) != 1 {
		t.Fatalf("mandatory snapshot: %+v", review)
	}
	revision := review.Revisions[0]
	repo := review.Repositories[0]
	var file domain.ReviewFileContent
	c.mustDo(http.MethodGet, path+"/file?"+url.Values{"revisionId": {revision.ID}, "repositoryId": {repo.ID}, "path": {"values.yaml"}}.Encode(), nil, &file, http.StatusOK)
	if file.Before != "replicas: 1\n" || file.After != "replicas: 2\n" {
		t.Fatalf("wrong immutable content: %+v", file)
	}
	act(service.ReviewAction{Action: "comment", RevisionID: revision.ID, RepositoryID: repo.ID, Path: "values.yaml", Side: "after", StartLine: 1, EndLine: 1, Body: "Use three replicas"})
	if review.Threads[0].Excerpt != "replicas: 2" || !review.Threads[0].Messages[0].Draft {
		t.Fatal("draft lost its exact code anchor")
	}
	other := c.asUser("mallory")
	if status := other.do(http.MethodGet, path, nil, nil); status != http.StatusNotFound {
		t.Fatalf("review leaked to another owner: %d", status)
	}
	var queued domain.Job
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "A later independent task"}, &queued, http.StatusCreated)
	if status := c.jobStatus(session, string(queued.ID)); status != "QUEUED" {
		t.Fatalf("mandatory review did not hold queue: %s", status)
	}
	stale := service.ReviewAction{Action: "resolve", ExpectedVersion: review.Version - 1, ThreadID: review.Threads[0].ID, Resolved: true}
	if status := c.do(http.MethodPost, path, stale, nil); status != http.StatusConflict {
		t.Fatalf("stale client overwrote review: %d", status)
	}
	batch := uuid.NewString()
	act(service.ReviewAction{Action: "publish", RevisionID: revision.ID, BatchID: batch})
	correction := review.CorrectionJobID
	if correction == "" {
		t.Fatal("publication did not create correction job")
	}
	// Retry the identical publication even with an old expectedVersion.
	c.mustDo(http.MethodPost, path, service.ReviewAction{Action: "publish", ExpectedVersion: 0, RevisionID: revision.ID, BatchID: batch}, &review, http.StatusOK)
	waitUntil(t, "correction completed", func() bool { return c.jobStatus(session, string(correction)) == "COMPLETED" })
	read()
	if review.Status != "WAITING" || !review.Required {
		t.Fatalf("correction escaped validation: %+v", review)
	}
	if c.jobStatus(session, string(queued.ID)) != "QUEUED" {
		t.Fatal("correction also released independent queued work")
	}
	if len(c.snapshot(session).Jobs) != 3 {
		t.Fatal("retried publication created duplicate work")
	}
	if review.Threads[0].Messages[0].Draft || review.Threads[0].Messages[0].BatchID != batch {
		t.Fatal("publication lost immutable batch")
	}
	act(service.ReviewAction{Action: "refresh"})
	latest := review.Revisions[len(review.Revisions)-1]
	act(service.ReviewAction{Action: "resolve", ThreadID: review.Threads[0].ID, Resolved: true})
	// Approving an old capture after another edit must fail, without releasing work.
	if err := os.WriteFile(filepath.Join(repos[0], "values.yaml"), []byte("replicas: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if status := c.do(http.MethodPost, path, service.ReviewAction{Action: "approve", ExpectedVersion: review.Version, RevisionID: latest.ID}, nil); status != http.StatusConflict {
		t.Fatalf("approved changed code: %d", status)
	}
	read()
	latest = review.Revisions[len(review.Revisions)-1]
	act(service.ReviewAction{Action: "approve", RevisionID: latest.ID})
	waitUntil(t, "queued work released after approval", func() bool { return c.jobStatus(session, string(queued.ID)) == "COMPLETED" })
}

func TestReviewComparisonIncludesAFileRevertedSinceThePreviousPass(t *testing.T) {
	t.Parallel()
	repo := reviewRepository(t)
	c := newCore(t)
	project := c.createProject("comparison")
	backend, credential := c.registerBackend("comparison backend")
	c.connectBackendWith(reviewEditor{repos: []string{repo}}, credential, t.TempDir())
	session := c.startSession(project, backend, "", "Edit configuration")
	path := "/api/v1/sessions/" + session + "/review"
	var review domain.Review
	waitUntil(t, "automatic initial capture", func() bool {
		c.mustDo(http.MethodGet, path, nil, &review, http.StatusOK)
		return len(review.Revisions) > 0
	})
	if err := os.WriteFile(filepath.Join(repo, "transient.txt"), []byte("reviewed addition\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.mustDo(http.MethodPost, path, service.ReviewAction{Action: "refresh", ExpectedVersion: review.Version}, &review, http.StatusOK)
	previous := review.Revisions[len(review.Revisions)-1].ID
	if err := os.Remove(filepath.Join(repo, "transient.txt")); err != nil {
		t.Fatal(err)
	}
	c.mustDo(http.MethodPost, path, service.ReviewAction{Action: "refresh", ExpectedVersion: review.Version}, &review, http.StatusOK)
	latest := review.Revisions[len(review.Revisions)-1]
	repository := review.Repositories[0]
	for _, file := range latest.Repositories[0].Files {
		if file.Path == "transient.txt" {
			t.Fatal("transient file should be absent from the cumulative diff")
		}
	}
	q := url.Values{"revisionId": {latest.ID}, "compareTo": {previous}}
	var comparison domain.Review
	c.mustDo(http.MethodGet, path+"?"+q.Encode(), nil, &comparison, http.StatusOK)
	compared := comparison.Revisions[len(comparison.Revisions)-1].Repositories[0]
	if len(compared.Files) != 1 || compared.Files[0].Path != "transient.txt" || compared.Files[0].State != "DELETED" {
		t.Fatalf("reverted file missing: %+v", compared)
	}
	q.Set("repositoryId", repository.ID)
	q.Set("path", "transient.txt")
	var content domain.ReviewFileContent
	c.mustDo(http.MethodGet, path+"/file?"+q.Encode(), nil, &content, http.StatusOK)
	if content.Before != "reviewed addition\n" || content.After != "" {
		t.Fatalf("wrong previous-pass content: %+v", content)
	}
}

type reviewPauseRunner struct {
	repo      string
	paused    chan string
	continued chan string
}

func (r reviewPauseRunner) Run(ctx context.Context, p runner.StartParams, s runner.Sink) error {
	if err := s.JobStarted(ctx, p.RunID, p.JobID); err != nil {
		return err
	}
	caller := s.(reviewCaller)
	if _, err := caller.CallCoreTool(ctx, p.JobID, "review_repository_register", map[string]any{"directory": r.repo}, ""); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.repo, "values.yaml"), []byte("replicas: 2\n"), 0600); err != nil {
		return err
	}
	if _, err := caller.CallCoreTool(ctx, p.JobID, "review_request", map[string]any{"required": true}, ""); err != nil {
		return err
	}
	r.paused <- p.JobID
	if err := s.(interface {
		ReviewCheckpoint(context.Context, string) error
	}).ReviewCheckpoint(ctx, p.JobID); err != nil {
		return err
	}
	r.continued <- p.JobID
	return s.JobCompleted(ctx, p.RunID, p.JobID, "Approved continuation", nil)
}
func (reviewPauseRunner) Cancel(string) error               { return runner.ErrUnknownJob }
func (reviewPauseRunner) Inject(string, string, bool) error { return runner.ErrUnknownJob }
func (reviewPauseRunner) Available() error                  { return nil }

func TestMandatoryReviewBlocksAnExecutingAgentUntilHumanApproval(t *testing.T) {
	t.Parallel()
	c := newCore(t)
	project := c.createProject("checkpoint")
	backend, credential := c.registerBackend("checkpoint backend")
	paused := make(chan string, 1)
	continued := make(chan string, 1)
	c.connectBackendWith(reviewPauseRunner{repo: reviewRepository(t), paused: paused, continued: continued}, credential, t.TempDir())
	session := c.startSession(project, backend, "", "Require a human review")
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("agent never reached review")
	}
	select {
	case <-continued:
		t.Fatal("agent continued without validation")
	case <-time.After(100 * time.Millisecond):
	}
	var review domain.Review
	path := "/api/v1/sessions/" + session + "/review"
	c.mustDo(http.MethodGet, path, nil, &review, http.StatusOK)
	if status := c.do(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Try to bypass the gate", "delivery": "NOW"}, nil); status != http.StatusConflict {
		t.Fatalf("interrupt bypassed gate: %d", status)
	}
	c.mustDo(http.MethodPost, path, service.ReviewAction{Action: "approve", ExpectedVersion: review.Version, RevisionID: review.Revisions[0].ID}, &review, http.StatusOK)
	select {
	case <-continued:
	case <-time.After(5 * time.Second):
		t.Fatal("approval did not wake the executing agent")
	}
	waitUntil(t, "agent finished", func() bool { jobs := c.snapshot(session).Jobs; return len(jobs) == 1 && jobs[0].Status == "COMPLETED" })
	if review.Status != "APPROVED" {
		t.Fatal(fmt.Sprintf("unexpected approval: %+v", review))
	}
}
