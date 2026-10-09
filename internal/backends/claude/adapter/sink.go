package adapter

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/internal/backends/claude/workspace"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// Adapter is the runner sink: it turns provider output into protocol events.
var _ runner.Sink = (*Adapter)(nil)

// send records an event durably and ships it to Core. Recording first is what
// makes a Core outage survivable.
func (a *Adapter) send(ctx context.Context, build func() (*backendv1.JobEvent, error)) error {
	sdk := a.sdk()
	if sdk == nil {
		return fmt.Errorf("the backend client is not bound yet")
	}
	event, err := build()
	if err != nil {
		return err
	}
	return sdk.SendEvent(ctx, event)
}

// JobStarted implements runner.Sink.
func (a *Adapter) JobStarted(ctx context.Context, runID, jobID string) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobStarted(ctx, runID, jobID)
	})
}

// NativeSessionBound implements runner.Sink. It is also recorded locally, so a
// backend restart still knows which provider session backs the Run.
func (a *Adapter) NativeSessionBound(ctx context.Context, runID, jobID, nativeSessionID string) error {
	if err := a.store.SaveRun(ctx, state.RunRecord{
		RunID:           runID,
		NativeSessionID: nativeSessionID,
		ResumeStatus:    backendv1.ResumeStatus_RESUME_STATUS_AVAILABLE,
	}); err != nil {
		a.logger.Error("cannot record the native session locally", slog.String("error", err.Error()))
	}
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().NativeSessionBound(ctx, runID, jobID, nativeSessionID)
	})
}

// AgentMessage implements runner.Sink.
func (a *Adapter) AgentMessage(ctx context.Context, runID, jobID, text string) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().AgentMessage(ctx, runID, jobID, text)
	})
}

// ToolStarted implements runner.Sink.
func (a *Adapter) ToolStarted(ctx context.Context, runID, jobID, callID, name string, input map[string]any) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ToolStarted(ctx, runID, jobID, callID, name, toStruct(input))
	})
}

// ToolCompleted implements runner.Sink.
func (a *Adapter) ToolCompleted(ctx context.Context, runID, jobID, callID, name string, output map[string]any) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ToolCompleted(ctx, runID, jobID, callID, name, toStruct(output))
	})
}

// ToolFailed implements runner.Sink.
func (a *Adapter) ToolFailed(ctx context.Context, runID, jobID, callID, name, message string) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ToolFailed(ctx, runID, jobID, callID, name, "TOOL_ERROR", message)
	})
}

// WorkspaceChanged implements runner.Sink. Core receives the list of paths and
// the line counts; the diff itself never leaves the backend (spec section 22).
func (a *Adapter) WorkspaceChanged(ctx context.Context, runID, jobID string, summary workspace.Summary) error {
	files := make([]*backendv1.FileChange, 0, len(summary.Files))
	for _, file := range summary.Files {
		files = append(files, &backendv1.FileChange{
			Path:  file.Path,
			State: fileState(file.State),
		})
	}

	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().WorkspaceChanged(ctx, runID, jobID, &backendv1.WorkspaceChanged{
			KnownDirectoryId: a.knownDirectoryOf(jobID),
			Files:            files,
			Additions:        summary.Additions,
			Deletions:        summary.Deletions,
			Directory:        summary.Directory,
			BaseTree:         summary.BaseTree,
			HeadTree:         summary.HeadTree,
		})
	})
}

// OnWorkspaceDiffRequest implements client.WorkspaceDiffer: the diff of one
// file a Job changed, computed when someone opens it and nowhere kept.
func (a *Adapter) OnWorkspaceDiffRequest(ctx context.Context, req *backendv1.WorkspaceDiffRequest) *backendv1.WorkspaceDiff {
	diff, err := workspace.DiffOf(ctx, req.GetDirectory(), req.GetBaseTree(), req.GetHeadTree(), req.GetPath())
	if err != nil {
		return &backendv1.WorkspaceDiff{Error: &backendv1.Error{Code: "UNAVAILABLE", Message: err.Error()}}
	}
	return &backendv1.WorkspaceDiff{Diff: diff.Text, Binary: diff.Binary, Truncated: diff.Truncated}
}

// OnRepositoryStatusRequest implements client.RepositoryReader: where a
// Session's working directory stands in git, read when someone looks.
func (a *Adapter) OnRepositoryStatusRequest(ctx context.Context, req *backendv1.RepositoryStatusRequest) *backendv1.RepositoryStatus {
	directory := req.GetDirectory()
	if directory == "" {
		// A Session with no working directory runs where this backend puts
		// unscoped work, and that is the directory it is asking about.
		directory = a.cfg.Claude.DefaultWorkingDirectory
	}
	repo, err := workspace.StatusOf(ctx, directory, req.GetFetch())
	if err != nil {
		return &backendv1.RepositoryStatus{Directory: directory, Error: &backendv1.Error{Code: "UNAVAILABLE", Message: err.Error()}}
	}
	status := &backendv1.RepositoryStatus{
		Directory:    repo.Directory,
		Tracked:      repo.Tracked,
		Branch:       repo.Branch,
		Head:         repo.Head,
		Upstream:     repo.Upstream,
		Ahead:        repo.Ahead,
		Behind:       repo.Behind,
		UpstreamGone: repo.UpstreamGone,
		Staged:       repo.Staged,
		Unstaged:     repo.Unstaged,
		Untracked:    repo.Untracked,
		Conflicted:   repo.Conflicted,
		FetchError:   repo.FetchError,
	}
	if !repo.FetchedAt.IsZero() {
		status.FetchedAt = timestamppb.New(repo.FetchedAt)
	}
	return status
}

// fileState maps a detected state onto the protocol enum.
func fileState(state workspace.State) backendv1.FileState {
	switch state {
	case workspace.StateAdded:
		return backendv1.FileState_FILE_STATE_ADDED
	case workspace.StateDeleted:
		return backendv1.FileState_FILE_STATE_DELETED
	case workspace.StateRenamed:
		return backendv1.FileState_FILE_STATE_RENAMED
	case workspace.StateModified:
		return backendv1.FileState_FILE_STATE_MODIFIED
	default:
		return backendv1.FileState_FILE_STATE_UNSPECIFIED
	}
}

// JobCompleted implements runner.Sink.
func (a *Adapter) JobCompleted(ctx context.Context, runID, jobID, summary string, usage *backendv1.Usage) error {
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_COMPLETED)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobCompleted(ctx, runID, jobID, summary, usage)
	})
}

// JobFailed implements runner.Sink.
func (a *Adapter) JobFailed(ctx context.Context, runID, jobID, code, message string, usage *backendv1.Usage) error {
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_FAILED)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobFailed(ctx, runID, jobID, code, message, usage)
	})
}

// JobCancelled implements runner.Sink. Core moves the Job to CANCELLED only
// when this arrives: the backend is the source of truth for what actually
// stopped.
func (a *Adapter) JobCancelled(ctx context.Context, runID, jobID string) error {
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_CANCELLED)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobCancelled(ctx, runID, jobID)
	})
}

// Progress implements runner.Sink. Ephemeral signals are dropped when Core is
// unreachable: they are liveness, never history.
func (a *Adapter) Progress(ctx context.Context, runID, jobID, kind, detail string) {
	sdk := a.sdk()
	if sdk == nil {
		return
	}
	sdk.SendEphemeral(ctx, sdk.Events().Ephemeral(runID, jobID, kind, detail))
}

// recordTerminal remembers how a Job ended, so a reconnection reports the truth
// rather than a Job that looks stuck.
func (a *Adapter) recordTerminal(ctx context.Context, runID, jobID string, status backendv1.JobStatus) {
	if err := a.store.SaveJob(ctx, state.JobRecord{JobID: jobID, RunID: runID, Status: status}); err != nil {
		a.logger.Error("cannot record the job outcome locally", slog.String("error", err.Error()))
	}
}

// toStruct converts a decoded JSON object for the wire, falling back to an empty
// one rather than failing an event over unrepresentable content.
func toStruct(value map[string]any) *structpb.Struct {
	if value == nil {
		return emptyStruct()
	}
	encoded, err := structpb.NewStruct(value)
	if err != nil {
		return emptyStruct()
	}
	return encoded
}

// knownDirectoryOf returns the KnownDirectory a Job is working in, when Core
// resolved one. It lets a change summary be attributed to a directory rather
// than to a bare path.
func (a *Adapter) knownDirectoryOf(jobID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if job, ok := a.jobs[jobID]; ok {
		return job.knownDirectoryID
	}
	return ""
}
