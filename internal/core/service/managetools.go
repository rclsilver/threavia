package service

import (
	"context"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/tools"
)

// maxArtifactReadBytes bounds the text of an artifact returned to an agent:
// past it, the content would crowd out the conversation it is read into.
const maxArtifactReadBytes = 256 << 10

// isManageTool reports whether name is one of the management tools below.
func isManageTool(name tools.Name) bool {
	switch name {
	case tools.NameDecisionList, tools.NameDecisionRead, tools.NameDecisionSetImportance, tools.NameDecisionDelete,
		tools.NameTaskList, tools.NameTaskRead, tools.NameTaskDependencyAdd, tools.NameTaskDependencyRemove, tools.NameTaskDelete,
		tools.NameArtifactList, tools.NameArtifactRead, tools.NameArtifactRename, tools.NameArtifactDelete,
		tools.NameSessionControl, tools.NameSessionDelete, tools.NameJobDelete, tools.NameSessionFiles, tools.NameSessionDiff,
		tools.NameSessionRepository, tools.NameSessionPin, tools.NameSessionSnooze, tools.NameSessionMarkRead, tools.NameAttentionList,
		tools.NameProjectList, tools.NameProjectRead, tools.NameProjectCreate, tools.NameProjectUpdate, tools.NameProjectArchive, tools.NameProjectDelete,
		tools.NameAuditRead:
		return true
	}
	return false
}

// manageTool runs a management tool. Every identifier is checked against the
// calling Job's owner by the store; validated tools have already passed
// requireHumanReceipt in invokeTool.
func (s *Service) manageTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	switch name {
	case tools.NameDecisionList, tools.NameDecisionRead, tools.NameDecisionSetImportance, tools.NameDecisionDelete:
		return s.decisionTool(ctx, identity, jc, name, input)
	case tools.NameTaskList, tools.NameTaskRead, tools.NameTaskDependencyAdd, tools.NameTaskDependencyRemove, tools.NameTaskDelete:
		return s.taskTool(ctx, identity, jc, name, input)
	case tools.NameArtifactList, tools.NameArtifactRead, tools.NameArtifactRename, tools.NameArtifactDelete:
		return s.artifactTool(ctx, identity, jc, name, input)
	case tools.NameProjectList, tools.NameProjectRead, tools.NameProjectCreate, tools.NameProjectUpdate, tools.NameProjectArchive, tools.NameProjectDelete:
		return s.projectTool(ctx, identity, jc, name, input)
	case tools.NameAuditRead:
		var projectID domain.ProjectID
		if _, present := input["projectId"]; present {
			id, err := sessionToolProject(input, "")
			if err != nil {
				return nil, err
			}
			if _, err := s.GetProject(ctx, identity, id); err != nil {
				return nil, err
			}
			projectID = id
		}
		limit, err := integerInput(input, "limit", 100, 1, 500)
		if err != nil {
			return nil, err
		}
		entries, err := s.Audit(ctx, identity, projectID, int(limit))
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"entries": entries})
	case tools.NameAttentionList:
		var sessionID domain.SessionID
		if raw := text(input, "sessionId"); raw != "" {
			session, err := s.ownedSession(ctx, identity, raw)
			if err != nil {
				return nil, err
			}
			sessionID = session.ID
		}
		attention, err := s.Attention(ctx, identity, sessionID)
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"attention": attention})
	case tools.NameJobDelete:
		jobID := domain.JobID(text(input, "jobId"))
		if !isUUID(string(jobID)) {
			return nil, fmt.Errorf("%w: a valid jobId is required", ErrInvalid)
		}
		// Withdrawn like the clients do, so the timeline says the message was
		// cancelled instead of showing it still waiting for an answer.
		job, err := s.WithdrawQueuedJob(ctx, identity, jobID, "Withdrawn by an agent after the person approved")
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"job": job, "withdrawn": true})
	}
	return s.sessionManageTool(ctx, identity, jc, name, input)
}

func decisionScope(decision domain.Decision) string {
	if decision.ProjectID == "" {
		return "global"
	}
	return "project"
}

func decisionEntry(decision domain.Decision, full bool) map[string]any {
	entry := map[string]any{
		"decisionId": string(decision.ID), "title": decision.Title,
		"importance": decision.Importance.String(), "status": decision.Status.String(),
		"scope": decisionScope(decision), "createdAt": decision.CreatedAt.Format(time.RFC3339),
		"recordedByAgent": decision.CreatedByJobID != nil,
	}
	if decision.ProjectID != "" {
		entry["projectId"] = string(decision.ProjectID)
	}
	if decision.Supersedes != nil {
		entry["supersedes"] = string(*decision.Supersedes)
	}
	if full || decision.Content != "" {
		entry["content"] = decision.Content
	}
	return entry
}

func (s *Service) decisionTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	if name == tools.NameDecisionList {
		target, err := sessionToolProject(input, jc.ProjectID)
		if err != nil {
			return nil, err
		}
		projectID, err := memoryProject(target, text(input, "scope"))
		if err != nil {
			return nil, err
		}
		if projectID != "" {
			if _, err := s.GetProject(ctx, identity, projectID); err != nil {
				return nil, err
			}
		}
		includeSuperseded, err := boolInput(input, "includeSuperseded", false)
		if err != nil {
			return nil, err
		}
		decisions, err := s.ListDecisions(ctx, identity, projectID, includeSuperseded)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(decisions))
		for _, decision := range decisions {
			out = append(out, decisionEntry(decision, false))
		}
		return map[string]any{"decisions": out}, nil
	}
	id := domain.DecisionID(text(input, "decisionId"))
	if !isUUID(string(id)) {
		return nil, fmt.Errorf("%w: a valid decision identifier is required", ErrInvalid)
	}
	decision, err := s.store.GetDecision(ctx, identity.UserID, id)
	if err != nil {
		return nil, translate(err)
	}
	switch name {
	case tools.NameDecisionRead:
		entry := decisionEntry(decision, true)
		// The chain is read from the decision's own scope: supersession never
		// crosses it, except a promotion, which supersedes from project memory.
		history, err := s.ListDecisions(ctx, identity, decision.ProjectID, true)
		if err != nil {
			return nil, err
		}
		byID := make(map[domain.DecisionID]domain.Decision, len(history))
		for _, other := range history {
			byID[other.ID] = other
			if other.Supersedes != nil && *other.Supersedes == decision.ID {
				entry["supersededBy"] = string(other.ID)
			}
		}
		var replaced []any
		for cursor := decision.Supersedes; cursor != nil && len(replaced) < 50; {
			previous, ok := byID[*cursor]
			if !ok {
				if previous, err = s.store.GetDecision(ctx, identity.UserID, *cursor); err != nil {
					break
				}
			}
			replaced = append(replaced, decisionEntry(previous, false))
			cursor = previous.Supersedes
		}
		if replaced != nil {
			entry["replaced"] = replaced
		}
		return entry, nil
	case tools.NameDecisionSetImportance:
		updated, err := s.SetDecisionImportance(ctx, identity, decision.ID, domain.DecisionImportance(strings.ToUpper(text(input, "importance"))))
		if err != nil {
			return nil, err
		}
		return decisionEntry(updated, false), nil
	case tools.NameDecisionDelete:
		if err := s.DeleteDecision(ctx, identity, decision.ID); err != nil {
			return nil, err
		}
		return map[string]any{"decisionId": string(decision.ID), "deleted": true}, nil
	}
	panic("unreachable decision tool")
}

func (s *Service) taskTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	if name == tools.NameTaskList {
		projectID, err := sessionToolProject(input, jc.ProjectID)
		if err != nil {
			return nil, err
		}
		if _, err := s.GetProject(ctx, identity, projectID); err != nil {
			return nil, err
		}
		includeDone, err := boolInput(input, "includeDone", false)
		if err != nil {
			return nil, err
		}
		status := domain.TaskStatus(strings.ToUpper(text(input, "status")))
		if status != "" && !status.Valid() {
			return nil, fmt.Errorf("%w: unknown task status %q", ErrInvalid, status)
		}
		tasks, err := s.ListTasks(ctx, identity, projectID, includeDone || status == domain.TaskDone)
		if err != nil {
			return nil, err
		}
		if status != "" {
			kept := tasks[:0]
			for _, task := range tasks {
				if task.Status == status {
					kept = append(kept, task)
				}
			}
			tasks = kept
		}
		return map[string]any{"tasks": summariseTasks(tasks)}, nil
	}
	id := domain.TaskID(text(input, "taskId"))
	if !isUUID(string(id)) {
		return nil, fmt.Errorf("%w: a valid task identifier is required", ErrInvalid)
	}
	task, err := s.store.GetTask(ctx, identity.UserID, id)
	if err != nil {
		return nil, translate(err)
	}
	switch name {
	case tools.NameTaskRead:
		all, err := s.ListTasks(ctx, identity, task.ProjectID, true)
		if err != nil {
			return nil, err
		}
		byID := make(map[domain.TaskID]domain.Task, len(all))
		var dependents []domain.Task
		for _, other := range all {
			byID[other.ID] = other
			for _, dep := range other.DependsOn {
				if dep == task.ID {
					dependents = append(dependents, other)
				}
			}
		}
		var dependsOn []domain.Task
		for _, dep := range task.DependsOn {
			if blocker, ok := byID[dep]; ok {
				dependsOn = append(dependsOn, blocker)
			}
		}
		entry := map[string]any{
			"taskId": string(task.ID), "projectId": string(task.ProjectID), "title": task.Title,
			"description": task.Description, "status": task.Status.String(),
			"createdAt": task.CreatedAt.Format(time.RFC3339), "updatedAt": task.UpdatedAt.Format(time.RFC3339),
			"dependsOn": summariseTasks(dependsOn), "dependents": summariseTasks(dependents),
		}
		if task.CompletedAt != nil {
			entry["completedAt"] = task.CompletedAt.Format(time.RFC3339)
		}
		return entry, nil
	case tools.NameTaskDependencyAdd, tools.NameTaskDependencyRemove:
		dependsOn := domain.TaskID(text(input, "dependsOn"))
		if !isUUID(string(dependsOn)) {
			return nil, fmt.Errorf("%w: a valid dependsOn task identifier is required", ErrInvalid)
		}
		var updated domain.Task
		if name == tools.NameTaskDependencyAdd {
			updated, err = s.AddTaskDependency(ctx, identity, task.ID, dependsOn)
		} else {
			updated, err = s.RemoveTaskDependency(ctx, identity, task.ID, dependsOn)
		}
		if err != nil {
			return nil, err
		}
		deps := make([]any, 0, len(updated.DependsOn))
		for _, dep := range updated.DependsOn {
			deps = append(deps, string(dep))
		}
		return map[string]any{"taskId": string(updated.ID), "status": updated.Status.String(), "dependsOn": deps}, nil
	case tools.NameTaskDelete:
		if err := s.DeleteTask(ctx, identity, task.ID); err != nil {
			return nil, err
		}
		return map[string]any{"taskId": string(task.ID), "deleted": true}, nil
	}
	panic("unreachable task tool")
}

func artifactEntry(artifact domain.Artifact) map[string]any {
	entry := map[string]any{
		"artifactId": string(artifact.ID), "projectId": string(artifact.ProjectID),
		"filename": artifact.Filename, "mimeType": artifact.MimeType, "size": artifact.Size,
		"sha256": artifact.SHA256, "createdAt": artifact.CreatedAt.Format(time.RFC3339),
	}
	if artifact.SessionID != nil {
		entry["sessionId"] = string(*artifact.SessionID)
	}
	if artifact.JobID != nil {
		entry["jobId"] = string(*artifact.JobID)
	}
	return entry
}

// textual says whether content of this type reads as text.
func textual(mimeType string) bool {
	base, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		base = mimeType
	}
	if strings.HasPrefix(base, "text/") {
		return true
	}
	for _, suffix := range []string{"json", "xml", "yaml", "javascript", "x-sh", "toml", "csv", "svg+xml"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// projectArtifact returns a file of one of the person's Projects. A Skill
// bundle, global or not, is the Skill's content, managed with the skill tools.
func (s *Service) projectArtifact(ctx context.Context, identity auth.Identity, raw string) (domain.Artifact, error) {
	if raw == "" || !isUUID(raw) {
		return domain.Artifact{}, fmt.Errorf("%w: a valid artifact identifier is required", ErrInvalid)
	}
	artifact, err := s.GetArtifact(ctx, identity, domain.ArtifactID(raw))
	if err != nil {
		return domain.Artifact{}, err
	}
	if artifact.ProjectID == "" {
		return domain.Artifact{}, fmt.Errorf("%w: artifact %s", ErrNotFound, raw)
	}
	bundle, err := s.store.IsSkillArtifact(ctx, artifact.ID)
	if err != nil {
		return domain.Artifact{}, translate(err)
	}
	if bundle {
		return domain.Artifact{}, fmt.Errorf("%w: artifact %s", ErrNotFound, raw)
	}
	return artifact, nil
}

// ReadArtifactText returns an Artifact's metadata and, when it is text, its
// content up to maxArtifactReadBytes. Binary content is never returned: an
// agent or a control client reads it as data in a conversation.
func (s *Service) ReadArtifactText(ctx context.Context, identity auth.Identity, raw string) (map[string]any, error) {
	artifact, err := s.projectArtifact(ctx, identity, raw)
	if err != nil {
		return nil, err
	}
	entry := artifactEntry(artifact)
	if !textual(artifact.MimeType) {
		entry["content"] = nil
		entry["note"] = "Binary content is not returned; the person can open it from the conversation or the project's artifacts."
		return entry, nil
	}
	_, body, err := s.OpenArtifact(ctx, identity, artifact.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, maxArtifactReadBytes+1))
	if err != nil {
		return nil, err
	}
	truncated := len(data) > maxArtifactReadBytes
	if truncated {
		data = data[:maxArtifactReadBytes]
		// Cut a rune the limit split, so the text stays valid.
		for i := 0; i < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		entry["content"] = nil
		entry["note"] = "The content is not valid UTF-8 text."
		return entry, nil
	}
	entry["content"] = string(data)
	entry["truncated"] = truncated
	return entry, nil
}

func (s *Service) artifactTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	if name == tools.NameArtifactList {
		projectID, err := sessionToolProject(input, jc.ProjectID)
		if err != nil {
			return nil, err
		}
		var sessionID *domain.SessionID
		if raw := text(input, "sessionId"); raw != "" {
			session, err := s.ownedSession(ctx, identity, raw)
			if err != nil {
				return nil, err
			}
			if _, present := input["projectId"]; !present {
				projectID = session.ProjectID
			} else if session.ProjectID != projectID {
				return nil, fmt.Errorf("%w: the session belongs to another project", ErrInvalid)
			}
			sessionID = &session.ID
		}
		if _, err := s.GetProject(ctx, identity, projectID); err != nil {
			return nil, err
		}
		limit, err := integerInput(input, "limit", 50, 1, 200)
		if err != nil {
			return nil, err
		}
		artifacts, err := s.ListArtifacts(ctx, identity, projectID, sessionID, int(limit))
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(artifacts))
		for _, artifact := range artifacts {
			out = append(out, artifactEntry(artifact))
		}
		return map[string]any{"artifacts": out}, nil
	}
	if name == tools.NameArtifactRead {
		return s.ReadArtifactText(ctx, identity, text(input, "artifactId"))
	}
	artifact, err := s.projectArtifact(ctx, identity, text(input, "artifactId"))
	if err != nil {
		return nil, err
	}
	id := artifact.ID
	switch name {
	case tools.NameArtifactRename:
		renamed, err := s.RenameArtifact(ctx, identity, id, text(input, "filename"))
		if err != nil {
			return nil, err
		}
		return artifactEntry(renamed), nil
	case tools.NameArtifactDelete:
		if err := s.DeleteArtifact(ctx, identity, id); err != nil {
			return nil, err
		}
		return map[string]any{"artifactId": string(id), "deleted": true}, nil
	}
	panic("unreachable artifact tool")
}

func projectEntry(project domain.Project, full bool) map[string]any {
	entry := map[string]any{"projectId": string(project.ID), "name": project.Name, "description": project.Description, "status": project.Status}
	if full {
		entry["instructions"] = project.Instructions
	}
	return entry
}

// projectDetail answers with one project in full, both instruction texts
// included. They are read beside the Project rather than taken from it: the
// supervision is not part of it, and after an instructions change the Project
// at hand still holds the old agent text.
func (s *Service) projectDetail(ctx context.Context, identity auth.Identity, project domain.Project) (map[string]any, error) {
	own, err := s.store.ProjectInstructionsOf(ctx, identity.UserID, project.ID)
	if err != nil {
		return nil, translate(err)
	}
	entry := projectEntry(project, true)
	entry["instructions"] = own.Agent
	entry["supervisionInstructions"] = own.Supervision
	return sessionToolResult(entry)
}

func (s *Service) projectTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	switch name {
	case tools.NameProjectList:
		includeArchived, err := boolInput(input, "includeArchived", false)
		if err != nil {
			return nil, err
		}
		projects, err := s.ListProjects(ctx, identity, includeArchived)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(projects))
		for _, project := range projects {
			entry := projectEntry(project, false)
			entry["current"] = project.ID == jc.ProjectID
			out = append(out, entry)
		}
		return sessionToolResult(map[string]any{"projects": out})
	case tools.NameProjectCreate:
		project, err := s.CreateProject(ctx, identity, text(input, "name"), text(input, "description"))
		if err != nil {
			return nil, err
		}
		return s.projectDetail(ctx, identity, project)
	}
	projectID, err := sessionToolProject(input, jc.ProjectID)
	if err != nil {
		return nil, err
	}
	project, err := s.GetProject(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	switch name {
	case tools.NameProjectRead:
		return s.projectDetail(ctx, identity, project)
	case tools.NameProjectUpdate:
		fields := map[string]string{}
		for _, key := range []string{"name", "description", "instructions"} {
			value, err := presentString(input, key)
			if err != nil {
				return nil, err
			}
			if value == nil {
				continue
			}
			// UpdateProject keeps a field given empty; a lone space is stored
			// trimmed, which is how the clients clear one.
			if strings.TrimSpace(*value) == "" {
				if key == "name" {
					return nil, fmt.Errorf("%w: a project name cannot be empty", ErrInvalid)
				}
				fields[key] = " "
				continue
			}
			fields[key] = *value
		}
		supervision, err := presentString(input, "supervisionInstructions")
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 && supervision == nil {
			return nil, fmt.Errorf("%w: give name, description, instructions or supervisionInstructions to change", ErrInvalid)
		}
		if supervision == nil {
			updated, err := s.UpdateProject(ctx, identity, project.ID, fields["name"], fields["description"], fields["instructions"])
			if err != nil {
				return nil, err
			}
			return s.projectDetail(ctx, identity, updated)
		}
		// The supervision is written with the agent text: when the call gives
		// both they land together, and when it gives only the supervision the
		// stored agent text is kept, as read under the project lock. The
		// supervision is checked before a name or description change so that
		// a refused text does not leave the call half applied.
		var agent *string
		if given, ok := fields["instructions"]; ok {
			agent = &given
			delete(fields, "instructions")
		}
		if err := (domain.Instructions{Supervision: strings.TrimSpace(*supervision)}).Validate(); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
		if len(fields) > 0 {
			if project, err = s.UpdateProject(ctx, identity, project.ID, fields["name"], fields["description"], ""); err != nil {
				return nil, err
			}
		}
		if _, err := s.setProjectInstructions(ctx, identity, project.ID, agent, *supervision); err != nil {
			return nil, err
		}
		return s.projectDetail(ctx, identity, project)
	case tools.NameProjectArchive:
		archived, err := boolInput(input, "archived", false)
		if err != nil {
			return nil, err
		}
		if _, present := input["archived"]; !present {
			return nil, fmt.Errorf("%w: archived is required", ErrInvalid)
		}
		status := domain.ProjectActive
		if archived {
			if project.ID == jc.ProjectID {
				return nil, fmt.Errorf("%w: a conversation cannot archive its own project", ErrConflict)
			}
			status = domain.ProjectArchived
		}
		if project.Status != status {
			if project, err = s.SetProjectStatus(ctx, identity, project.ID, status); err != nil {
				return nil, err
			}
		}
		return sessionToolResult(projectEntry(project, false))
	case tools.NameProjectDelete:
		if project.ID == jc.ProjectID {
			return nil, fmt.Errorf("%w: a conversation cannot delete its own project", ErrConflict)
		}
		if err := s.DeleteProject(ctx, identity, project.ID); err != nil {
			return nil, err
		}
		return map[string]any{"projectId": string(project.ID), "deleted": true}, nil
	}
	panic("unreachable project tool")
}

// supervises reports whether a session is this conversation or one of its
// direct delegates: the sessions it may act on without asking the person.
func supervises(jc jobScope, session domain.Session) bool {
	return session.ID == jc.SessionID || (session.ManagerSessionID != nil && *session.ManagerSessionID == jc.SessionID)
}

// sessionManageTool runs the session tools that act on any session of the
// person, as opposed to the direct-delegate supervision in sessiontools.go.
// Pinning, snoozing, marking read and fetching act without validation only on
// this conversation and its direct delegates; elsewhere they go through the
// validated session_control.
func (s *Service) sessionManageTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	session, err := s.ownedSession(ctx, identity, text(input, "sessionId"))
	if err != nil {
		return nil, err
	}
	unmanaged := func(action string) error {
		if supervises(jc, session) {
			return nil
		}
		return fmt.Errorf("%w: this conversation does not manage session %s; use session_control with action %s, which asks the person", ErrInvalid, session.ID, action)
	}
	switch name {
	case tools.NameSessionControl:
		switch action := text(input, "action"); action {
		case "cancel":
			return s.cancelSessionJob(ctx, identity, session, input)
		case "archive", "restore":
			return s.archiveSession(ctx, identity, session, action == "archive")
		case "update":
			return s.updateManagedSession(ctx, identity, session, input)
		case "answer":
			return s.answerSession(ctx, identity, session, input, nil)
		case "pin":
			return s.pinSession(ctx, identity, session, input)
		case "snooze":
			return s.snoozeSession(ctx, identity, session, input)
		case "mark_read":
			return s.markSessionRead(ctx, identity, session, input)
		case "fetch":
			return s.sessionRepository(ctx, identity, session, true)
		default:
			return nil, fmt.Errorf("%w: action must be cancel, archive, restore, update, answer, pin, snooze, mark_read or fetch", ErrInvalid)
		}
	case tools.NameSessionDelete:
		if session.ID == jc.SessionID {
			return nil, fmt.Errorf("%w: a conversation cannot delete itself", ErrConflict)
		}
		withArtifacts, err := boolInput(input, "deleteArtifacts", false)
		if err != nil {
			return nil, err
		}
		if err := s.DeleteSession(ctx, identity, session.ID, withArtifacts); err != nil {
			return nil, err
		}
		return map[string]any{"sessionId": string(session.ID), "deleted": true, "artifactsDeleted": withArtifacts}, nil
	case tools.NameSessionFiles:
		limit, err := integerInput(input, "limit", 0, 0, 200)
		if err != nil {
			return nil, err
		}
		files, err := s.FilesOf(ctx, identity, session.ID, text(input, "query"), int(limit))
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"files": files})
	case tools.NameSessionDiff:
		sequence, err := integerInput(input, "eventSequence", 0, 1, 1<<53-1)
		if err != nil {
			return nil, err
		}
		if _, present := input["eventSequence"]; !present || text(input, "path") == "" {
			return nil, fmt.Errorf("%w: eventSequence and path are required", ErrInvalid)
		}
		diff, err := s.FileDiffOf(ctx, identity, session.ID, domain.Sequence(sequence), text(input, "path"))
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"diff": diff})
	case tools.NameSessionRepository:
		fetch, err := boolInput(input, "fetch", false)
		if err != nil {
			return nil, err
		}
		if fetch {
			// A fetch reaches the network and moves remote refs on that machine.
			if err := unmanaged("fetch"); err != nil {
				return nil, err
			}
		}
		return s.sessionRepository(ctx, identity, session, fetch)
	case tools.NameSessionPin:
		if err := unmanaged("pin"); err != nil {
			return nil, err
		}
		return s.pinSession(ctx, identity, session, input)
	case tools.NameSessionSnooze:
		if err := unmanaged("snooze"); err != nil {
			return nil, err
		}
		return s.snoozeSession(ctx, identity, session, input)
	case tools.NameSessionMarkRead:
		if err := unmanaged("mark_read"); err != nil {
			return nil, err
		}
		return s.markSessionRead(ctx, identity, session, input)
	}
	return nil, fmt.Errorf("%w: unknown core tool %q", ErrInvalid, name)
}

func (s *Service) sessionRepository(ctx context.Context, identity auth.Identity, session domain.Session, fetch bool) (map[string]any, error) {
	repo, err := s.RepositoryOf(ctx, identity, session.ID, fetch)
	if err != nil {
		return nil, err
	}
	return sessionToolResult(map[string]any{"repository": repo})
}

func (s *Service) pinSession(ctx context.Context, identity auth.Identity, session domain.Session, input map[string]any) (map[string]any, error) {
	if _, present := input["pinned"]; !present {
		return nil, fmt.Errorf("%w: pinned is required", ErrInvalid)
	}
	pinned, err := boolInput(input, "pinned", false)
	if err != nil {
		return nil, err
	}
	updated, err := s.SetSessionPinned(ctx, identity, session.ID, pinned)
	if err != nil {
		return nil, err
	}
	return sessionToolResult(map[string]any{"session": updated})
}

func (s *Service) snoozeSession(ctx context.Context, identity auth.Identity, session domain.Session, input map[string]any) (map[string]any, error) {
	wake, err := boolInput(input, "clear", false)
	if err != nil {
		return nil, err
	}
	if wake {
		updated, err := s.UnsnoozeSession(ctx, identity, session.ID)
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"session": updated})
	}
	untilChange, err := boolInput(input, "untilChange", false)
	if err != nil {
		return nil, err
	}
	var until *time.Time
	if raw := text(input, "until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("%w: until must be an RFC 3339 time", ErrInvalid)
		}
		until = &parsed
	}
	updated, err := s.SnoozeSession(ctx, identity, session.ID, until, untilChange)
	if err != nil {
		return nil, err
	}
	return sessionToolResult(map[string]any{"session": updated})
}

func (s *Service) markSessionRead(ctx context.Context, identity auth.Identity, session domain.Session, input map[string]any) (map[string]any, error) {
	sequence, err := integerInput(input, "sequence", 0, 1, 1<<53-1)
	if err != nil {
		return nil, err
	}
	if sequence == 0 {
		latest, err := s.SessionHistory(ctx, identity, session.ID, 0, 1)
		if err != nil {
			return nil, err
		}
		if len(latest) == 0 {
			return map[string]any{"sessionId": string(session.ID), "read": true}, nil
		}
		sequence = int64(latest[len(latest)-1].Sequence)
	}
	if err := s.MarkSessionRead(ctx, identity, session.ID, domain.Sequence(sequence)); err != nil {
		return nil, err
	}
	return map[string]any{"sessionId": string(session.ID), "read": true, "sequence": sequence}, nil
}
