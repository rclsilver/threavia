package service

import (
	"encoding/json"
	"fmt"
)

// encodePayload serialises an event payload. Payloads stay small and structured;
// large content belongs in an Artifact, referenced by id.
func encodePayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return raw, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode event payload: %w", err)
	}
	return encoded, nil
}

// UserMessagePayload is the body of a user.message event. Messages are events:
// there is no separate message history.
type UserMessagePayload struct {
	Text string `json:"text"`
}

// AgentMessagePayload is the body of an agent.message event.
type AgentMessagePayload struct {
	Text        string   `json:"text"`
	ArtifactIDs []string `json:"artifactIds,omitempty"`
}

// SessionCreatedPayload is the body of a session.created event.
type SessionCreatedPayload struct {
	Title              string  `json:"title"`
	WorkingDirectoryID *string `json:"workingDirectoryId,omitempty"`
}

// SessionRenamedPayload is the body of a session.renamed event.
type SessionRenamedPayload struct {
	Title string `json:"title"`
}

// RunCreatedPayload is the body of a run.created event. A Run is an
// infrastructure detail: clients show it only when the user needs to choose or
// debug a backend.
type RunCreatedPayload struct {
	BackendInstanceID string `json:"backendInstanceId"`
}

// JobCreatedPayload is the body of a job.created event.
type JobCreatedPayload struct {
	RunID string `json:"runId"`
}

// JobEndedPayload is the body of job.completed, job.failed and job.cancelled.
type JobEndedPayload struct {
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// ToolPayload is the body of the tool.* events.
type ToolPayload struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// ValidationRequestedPayload is the body of a validation.requested event.
type ValidationRequestedPayload struct {
	ValidationID  string          `json:"validationId"`
	Title         string          `json:"title"`
	Summary       string          `json:"summary,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	PayloadSHA256 string          `json:"payloadSha256"`
	Humanized     string          `json:"humanized,omitempty"`
}

// ValidationResolvedPayload is the body of a validation.resolved event, and the
// audit receipt of specification section 16.
type ValidationResolvedPayload struct {
	ValidationID  string `json:"validationId"`
	Approved      bool   `json:"approved"`
	ActorUserID   string `json:"actorUserId"`
	Channel       string `json:"channel"`
	PayloadSHA256 string `json:"payloadSha256"`
	Note          string `json:"note,omitempty"`
}

// UserInputRequestedPayload is the body of a user_input.requested event.
type UserInputRequestedPayload struct {
	RequestID string   `json:"requestId"`
	Prompt    string   `json:"prompt"`
	Choices   []string `json:"choices,omitempty"`
	FreeText  bool     `json:"freeText"`
}

// UserInputResolvedPayload is the body of a user_input.resolved event.
type UserInputResolvedPayload struct {
	RequestID   string `json:"requestId"`
	Value       string `json:"value"`
	ActorUserID string `json:"actorUserId"`
	Channel     string `json:"channel"`
}

// WorkspaceChangedPayload is the body of a workspace.changed event. Core keeps
// the list of files touched; the detailed diff stays backend-owned.
type WorkspaceChangedPayload struct {
	KnownDirectoryID string          `json:"knownDirectoryId,omitempty"`
	Files            []WorkspaceFile `json:"files,omitempty"`
	Additions        int32           `json:"additions"`
	Deletions        int32           `json:"deletions"`
}

// WorkspaceFile is one entry of a workspace change summary.
type WorkspaceFile struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// WorkingDirectoryChangedPayload is the body of a working_directory.changed
// event.
type WorkingDirectoryChangedPayload struct {
	KnownDirectoryID *string `json:"knownDirectoryId,omitempty"`
}

// jsonUnmarshal is a thin wrapper keeping the encoding/json dependency in one
// place.
func jsonUnmarshal(data []byte, target any) error { return json.Unmarshal(data, target) }

// TaskPayload is the body of the task.* events.
type TaskPayload struct {
	TaskID string `json:"taskId"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// DecisionPayload is the body of the decision.* events.
type DecisionPayload struct {
	DecisionID string `json:"decisionId"`
	Title      string `json:"title"`
	Importance string `json:"importance"`
	// SupersededBy is set on decision.superseded.
	SupersededBy string `json:"supersededBy,omitempty"`
}

// mustJSON encodes a value for an audit detail. The values passed are small
// structs with no unencodable field, so a failure is a programming error.
func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
