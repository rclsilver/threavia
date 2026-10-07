package adapter

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
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
func (a *Adapter) AskUser(ctx context.Context, jobID, prompt string, choices []string) (string, error) {
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
			FreeText:  len(choices) == 0,
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
func (a *Adapter) CallCoreTool(ctx context.Context, jobID, name string, input map[string]any) (map[string]any, error) {
	if !a.knows(jobID) {
		return nil, fmt.Errorf("job %s is not running here", jobID)
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
