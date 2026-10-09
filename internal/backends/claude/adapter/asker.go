package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
	"github.com/rclsilver/threavia/internal/backends/claude/policy"
	"github.com/rclsilver/threavia/pkg/backend-sdk/tools"
)

// Adapter answers the local tool endpoint by asking the actual user, through
// Core.
var _ mcp.Asker = (*Adapter)(nil)

// AskPermission raises a ValidationRequest and blocks until it is resolved.
//
// There is no timeout, by design: the user may be on another device, and a
// permission request waits as long as it takes (spec section 16).
func (a *Adapter) AskPermission(ctx context.Context, jobID, toolName string, input map[string]any) (mcp.Decision, error) {
	// The policy answers first. A forbidden action is refused outright rather
	// than offered to the user as a choice: section 17 is explicit that a policy
	// is enforced, not suggested.
	switch decision := a.policyOf(jobID).Evaluate(toolName, input); decision.Verdict {
	case policy.Deny:
		a.logger.Info("refused by the execution policy",
			slog.String("jobId", jobID), slog.String("tool", toolName))
		return mcp.Decision{Approved: false, Reason: decision.Reason}, nil
	case policy.Allow:
		return mcp.Decision{Approved: true}, nil
	}

	requestID := uuid.NewString()
	w, err := a.registerWaiter(jobID, requestID)
	if err != nil {
		return mcp.Decision{}, err
	}
	defer a.takeWaiter(requestID)

	runID := a.runOf(jobID)
	err = a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ValidationRequested(ctx, runID, jobID, &backendv1.ValidationRequested{
			RequestId: requestID,
			Title:     permissionTitle(toolName, input),
			Summary:   fmt.Sprintf("The agent wants to use %s.", toolName),
			// The canonical technical payload: its hash is the security
			// reference of the validation receipt Core records.
			RequestPayload: toStruct(map[string]any{"tool": toolName, "input": input}),
		})
	})
	if err != nil {
		return mcp.Decision{}, err
	}

	select {
	case decision := <-w.approved:
		return decision, nil
	case err := <-w.failed:
		return mcp.Decision{}, err
	case <-ctx.Done():
		return mcp.Decision{}, ctx.Err()
	}
}

// AskUser raises a UserInputRequest and blocks until it is answered.
//
// freeText comes from the caller rather than from the absence of choices: a
// question that offers a shortcut may still need an answer that is none of
// them, and a client can only show the field if the request says so.
func (a *Adapter) AskUser(ctx context.Context, jobID, prompt string, choices []string, freeText bool) (string, error) {
	requestID := uuid.NewString()
	w, err := a.registerWaiter(jobID, requestID)
	if err != nil {
		return "", err
	}
	defer a.takeWaiter(requestID)

	runID := a.runOf(jobID)
	err = a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().UserInputRequested(ctx, runID, jobID, &backendv1.UserInputRequested{
			RequestId: requestID,
			Prompt:    prompt,
			Choices:   choices,
			FreeText:  freeText,
		})
	})
	if err != nil {
		return "", err
	}

	select {
	case answer := <-w.answer:
		return answer, nil
	case err := <-w.failed:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// registerWaiter records a pending request against its Job, so it is released
// if the Job ends first.
func (a *Adapter) registerWaiter(jobID, requestID string) (*waiter, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	job, ok := a.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job %s is not running here", jobID)
	}

	w := &waiter{
		jobID:    jobID,
		approved: make(chan mcp.Decision, 1),
		answer:   make(chan string, 1),
		failed:   make(chan error, 1),
	}
	a.waiters[requestID] = w
	job.requests[requestID] = struct{}{}
	return w, nil
}

// runOf returns the Run a Job belongs to.
func (a *Adapter) runOf(jobID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if job, ok := a.jobs[jobID]; ok {
		return job.runID
	}
	return ""
}

// permissionTitle renders a short, human-readable summary of what is being
// asked. The structured payload remains the technical reference.
func permissionTitle(toolName string, input map[string]any) string {
	for _, key := range []string{"file_path", "path", "command", "url", "pattern"} {
		if value, ok := input[key].(string); ok && value != "" {
			return fmt.Sprintf("%s: %s", toolName, value)
		}
	}
	return toolName
}

// CallCoreTool runs a Core Tool through the control stream and returns its
// result.
//
// Unlike a permission or a question, nothing human is involved: this is Core
// answering Core. It still goes over the same stream, because the backend holds
// no project knowledge of its own and must not pretend to.
func (a *Adapter) CallCoreTool(ctx context.Context, jobID, name string, input map[string]any, fileInput string) (map[string]any, error) {
	if !a.knows(jobID) {
		return nil, fmt.Errorf("job %s is not running here", jobID)
	}

	var file []byte
	if fileInput != "" {
		path, _ := input[fileInput].(string)
		content, filename, err := a.readPublishable(jobID, path)
		if err != nil {
			return nil, err
		}
		file = content
		input["filename"] = filename
	}

	encoded, err := structpb.NewStruct(input)
	if err != nil {
		return nil, fmt.Errorf("encode the tool input: %w", err)
	}

	result, err := a.sdk().Invoke(ctx, tools.Call{
		RunID: a.runOf(jobID),
		JobID: jobID,
		Name:  name,
		Input: encoded,
		File:  file,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{}, nil
	}
	return result.AsMap(), nil
}

// knows reports whether a Job is running on this backend.
func (a *Adapter) knows(jobID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.jobs[jobID]
	return ok
}

// policyOf returns what a Job is allowed to do. A Job this backend does not know
// gets the restrained default rather than a permissive one.
func (a *Adapter) policyOf(jobID string) policy.Policy {
	a.mu.Lock()
	defer a.mu.Unlock()
	if job, ok := a.jobs[jobID]; ok {
		return job.policy
	}
	return policy.From(nil)
}

// maxPublishBytes is the largest file the backend sends Core for an agent. Core
// refuses more anyway; checking here saves reading and sending it.
const maxPublishBytes = 10 << 20

// readPublishable reads a file an agent asked to hand to Core.
//
// Only from the Job's own directories — where it runs, and its scratch
// directory — after resolving links, so that a path cannot lead out of them;
// and never a file the Job's policy refuses to read. Publishing is reading on
// the agent's behalf, and it must not reach what the agent could not.
func (a *Adapter) readPublishable(jobID, path string) ([]byte, string, error) {
	a.mu.Lock()
	job, ok := a.jobs[jobID]
	var workingDirectory string
	var p policy.Policy
	if ok {
		workingDirectory, p = job.workingDirectory, job.policy
	}
	a.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("job %s is not running here", jobID)
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", errors.New("say which file to publish: path is empty")
	}
	if !filepath.IsAbs(path) {
		if workingDirectory == "" {
			return nil, "", fmt.Errorf("%s is relative and this job has no working directory; give an absolute path", path)
		}
		path = filepath.Join(workingDirectory, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", path, err)
	}

	var roots []string
	for _, root := range []string{workingDirectory, p.Scratch} {
		if root == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			roots = append(roots, real)
		}
	}
	inside := false
	for _, root := range roots {
		if rel, err := filepath.Rel(root, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			inside = true
			break
		}
	}
	if !inside {
		return nil, "", fmt.Errorf("only a file in the working directory (%s) or the scratch directory (%s) can be published; "+
			"write or copy it there first", workingDirectory, p.Scratch)
	}
	if reason, refused := p.RefusesReading(resolved, workingDirectory); refused {
		return nil, "", errors.New(reason)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s is not a file", path)
	}
	if info.Size() > maxPublishBytes {
		return nil, "", fmt.Errorf("%s is %d bytes; a published file may be up to %d", path, info.Size(), maxPublishBytes)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	return content, filepath.Base(resolved), nil
}
