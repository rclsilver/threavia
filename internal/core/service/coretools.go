package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/tools"
)

// CoreToolRequest executes a Core Tool on behalf of an agent.
//
// The call arrives scoped to a Job, which is what gives it an identity: the
// Project it may touch and the user it acts for are read from that Job, never
// from the request. An agent therefore cannot reach another project's knowledge
// by asking nicely, and a backend cannot speak for a Run it does not execute.
func (s *Service) CoreToolRequest(ctx context.Context, instanceID domain.BackendInstanceID, request *backendv1.CoreToolRequest) (*structpb.Struct, error) {
	jc, err := s.store.LoadJobContext(ctx, domain.JobID(request.GetJobId()))
	if err != nil {
		return nil, fmt.Errorf("unknown job %s", request.GetJobId())
	}
	if jc.BackendInstanceID != instanceID {
		return nil, fmt.Errorf("backend %s does not own job %s", instanceID, request.GetJobId())
	}

	identity := auth.Identity{UserID: jc.OwnerID}
	scope := jobScope{
		ProjectID:         jc.ProjectID,
		SessionID:         jc.SessionID,
		JobID:             jc.Job.ID,
		BackendInstanceID: jc.BackendInstanceID,
	}
	input := request.GetInput().AsMap()

	s.logger.Info("core tool invoked",
		slog.String("tool", request.GetName()),
		slog.String("jobId", request.GetJobId()),
		slog.String("projectId", string(jc.ProjectID)))

	result, err := s.invokeTool(ctx, identity, scope, tools.Name(request.GetName()), input, request.GetFile())
	if err != nil {
		return nil, err
	}
	return structpb.NewStruct(result)
}

// invokeTool dispatches one Core Tool call.
func (s *Service) invokeTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any, file []byte) (map[string]any, error) {
	switch name {
	case tools.NameArtifactPublish:
		return s.publishArtifact(ctx, identity, jc, input, file)

	case tools.NameProjectHistorySearch:
		hits, err := s.SearchHistory(ctx, identity, jc.ProjectID, text(input, "query"), 0)
		if err != nil {
			return nil, err
		}
		found := make([]any, 0, len(hits))
		for _, hit := range hits {
			found = append(found, map[string]any{
				"when":    hit.Envelope.Timestamp.Format("2006-01-02"),
				"type":    hit.Envelope.Type.String(),
				"excerpt": hit.Excerpt,
			})
		}
		return map[string]any{"results": found}, nil

	case tools.NameDecisionCreate:
		var supersedes *domain.DecisionID
		if raw := text(input, "supersedes"); raw != "" {
			id := domain.DecisionID(raw)
			supersedes = &id
		}
		decision, err := s.CreateDecision(ctx, identity, jc.ProjectID,
			text(input, "title"), text(input, "content"),
			domain.DecisionImportance(strings.ToUpper(text(input, "importance"))), supersedes)
		if err != nil {
			return nil, err
		}
		return map[string]any{"decisionId": string(decision.ID), "status": decision.Status.String()}, nil

	case tools.NameTaskCreate:
		var dependsOn []domain.TaskID
		for _, raw := range stringList(input, "dependsOn") {
			dependsOn = append(dependsOn, domain.TaskID(raw))
		}
		task, err := s.CreateTask(ctx, identity, jc.ProjectID,
			text(input, "title"), text(input, "description"), dependsOn)
		if err != nil {
			return nil, err
		}
		// Linking the Job that filed it keeps the trail from work to task.
		if err := s.store.LinkJobToTask(ctx, jc.JobID, task.ID); err != nil {
			s.logger.Warn("cannot link the job to the task it filed", slog.String("error", err.Error()))
		}
		return map[string]any{"taskId": string(task.ID), "status": task.Status.String()}, nil

	case tools.NameTaskReady:
		ready, err := s.ReadyTasks(ctx, identity, jc.ProjectID, 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tasks": summariseTasks(ready)}, nil

	case tools.NameTaskSearch:
		found, err := s.SearchTasks(ctx, identity, jc.ProjectID, text(input, "query"), 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tasks": summariseTasks(found)}, nil

	case tools.NameTaskUpdate:
		task, err := s.updateScopedTask(ctx, identity, jc, text(input, "taskId"),
			text(input, "title"), text(input, "description"),
			domain.TaskStatus(strings.ToUpper(text(input, "status"))))
		if err != nil {
			return nil, err
		}
		return map[string]any{"taskId": string(task.ID), "status": task.Status.String()}, nil

	case tools.NameTaskComplete:
		task, err := s.updateScopedTask(ctx, identity, jc, text(input, "taskId"), "", "", domain.TaskDone)
		if err != nil {
			return nil, err
		}
		if err := s.store.LinkJobToTask(ctx, jc.JobID, task.ID); err != nil {
			s.logger.Warn("cannot link the job to the task it completed", slog.String("error", err.Error()))
		}
		ready, err := s.ReadyTasks(ctx, identity, jc.ProjectID, 0)
		if err != nil {
			return nil, err
		}
		// Reporting what this unblocked is the whole point of the dependency
		// graph, so the agent does not have to ask again.
		return map[string]any{"taskId": string(task.ID), "unblocked": summariseTasks(ready)}, nil

	case tools.NameKnownDirectoryRegister:
		dir, err := s.CreateKnownDirectory(ctx, identity, jc.ProjectID,
			text(input, "name"), text(input, "description"), optionalString(text(input, "gitRemote")))
		if err != nil {
			return nil, err
		}
		if _, err := s.BindKnownDirectory(ctx, identity, dir.ID, jc.BackendInstanceID, text(input, "path")); err != nil {
			return nil, err
		}
		return map[string]any{"knownDirectoryId": string(dir.ID)}, nil

	case tools.NameKnownDirectoryBind:
		binding, err := s.BindKnownDirectory(ctx, identity,
			domain.KnownDirectoryID(text(input, "knownDirectoryId")),
			jc.BackendInstanceID, text(input, "path"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"knownDirectoryId": string(binding.KnownDirectoryID), "path": binding.Path}, nil

	case tools.NameWorkingDirectorySet:
		id := domain.KnownDirectoryID(text(input, "knownDirectoryId"))
		session, err := s.SetSessionWorkingDirectory(ctx, identity, jc.SessionID, &id)
		if err != nil {
			return nil, err
		}
		// It is the initial directory of future Runs, not of this one.
		return map[string]any{
			"sessionId": string(session.ID),
			"note":      "This takes effect from the next run; it is not a cd for the current one.",
		}, nil

	default:
		return nil, fmt.Errorf("%w: unknown core tool %q", ErrInvalid, name)
	}
}

// updateScopedTask changes a Task after checking it belongs to the Job's
// Project, so a tool call cannot reach across projects.
func (s *Service) updateScopedTask(ctx context.Context, identity auth.Identity, jc jobScope, taskID, title, description string, status domain.TaskStatus) (domain.Task, error) {
	if taskID == "" {
		return domain.Task{}, fmt.Errorf("%w: a task identifier is required", ErrInvalid)
	}
	current, err := s.store.GetTask(ctx, identity.UserID, domain.TaskID(taskID))
	if err != nil {
		return domain.Task{}, translate(err)
	}
	if current.ProjectID != jc.ProjectID {
		return domain.Task{}, fmt.Errorf("%w: task %s belongs to another project", ErrInvalid, taskID)
	}
	return s.UpdateTask(ctx, identity, current.ID, title, description, status)
}

// summariseTasks renders tasks compactly: a tool result an agent reads should
// not be a database dump.
func summariseTasks(tasks []domain.Task) []any {
	out := make([]any, 0, len(tasks))
	for _, task := range tasks {
		entry := map[string]any{
			"taskId": string(task.ID),
			"title":  task.Title,
			"status": task.Status.String(),
		}
		if task.Description != "" {
			entry["description"] = task.Description
		}
		out = append(out, entry)
	}
	return out
}

// text reads a string field from a tool input.
func text(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

// strings reads a list of strings from a tool input.
func stringList(input map[string]any, key string) []string {
	raw, ok := input[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && value != "" {
			out = append(out, value)
		}
	}
	return out
}

// jobScope is what a Core Tool call is allowed to act within: the Project, the
// Session and the backend of the Job that issued it. Nothing in a tool input can
// widen it.
type jobScope struct {
	ProjectID         domain.ProjectID
	SessionID         domain.SessionID
	JobID             domain.JobID
	BackendInstanceID domain.BackendInstanceID
}
