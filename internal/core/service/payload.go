package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/core/domain"
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
		return withoutNUL(raw)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode event payload: %w", err)
	}
	return withoutNUL(encoded)
}

// withoutNUL replaces every NUL character in the strings of a JSON payload with
// U+FFFD.
//
// PostgreSQL refuses \u0000 in a jsonb value, and a payload carrying one — a
// tool's output read from a binary file, say — failed the whole insert: the
// event was lost from the timeline, and a job event stayed unacknowledged for
// the backend to replay into the same refusal. A NUL means nothing to a person
// reading a timeline, so it is shown as the replacement character instead.
//
// The payload is decoded and encoded again rather than edited as text: in the
// encoded form, `\\u0000` is a backslash followed by the letters u0000, which
// must stay as they are.
func withoutNUL(encoded json.RawMessage) (json.RawMessage, error) {
	if !bytes.Contains(encoded, []byte(`\u0000`)) {
		return encoded, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	// Numbers are kept as written, so a large id does not become a float.
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("read event payload: %w", err)
	}
	cleaned, err := json.Marshal(replaceNUL(value))
	if err != nil {
		return nil, fmt.Errorf("encode event payload: %w", err)
	}
	return cleaned, nil
}

func replaceNUL(value any) any {
	switch typed := value.(type) {
	case string:
		return strings.ReplaceAll(typed, "\x00", "�")
	case []any:
		for index, item := range typed {
			typed[index] = replaceNUL(item)
		}
		return typed
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[strings.ReplaceAll(key, "\x00", "�")] = replaceNUL(item)
		}
		return out
	default:
		return value
	}
}

// UserMessagePayload is the body of a user.message event. Messages are events:
// there is no separate message history.
type UserMessagePayload struct {
	Text       string       `json:"text"`
	ActorJobID domain.JobID `json:"actorJobId,omitempty"`
	// Delivery says how a message sent while a Job ran reached it: NOW or
	// NEXT. Absent for a message that started a Job of its own.
	Delivery Delivery `json:"delivery,omitempty"`
	// ScheduleID names the Schedule that sent the message, when nobody typed
	// it.
	ScheduleID domain.ScheduleID `json:"scheduleId,omitempty"`
}

// ScheduleSkippedPayload is the body of a schedule.skipped event: a scheduled
// message that was not sent, and why.
type ScheduleSkippedPayload struct {
	ScheduleID domain.ScheduleID      `json:"scheduleId"`
	Outcome    domain.ScheduleOutcome `json:"outcome"`
	Reason     string                 `json:"reason"`
	Due        string                 `json:"due"`
}

// AgentMessagePayload is the body of an agent.message event.
type AgentMessagePayload struct {
	Text        string   `json:"text"`
	ArtifactIDs []string `json:"artifactIds,omitempty"`
}

// SessionCreatedPayload is the body of a session.created event.
type SessionCreatedPayload struct {
	Title              string            `json:"title"`
	WorkingDirectoryID *string           `json:"workingDirectoryId,omitempty"`
	ManagerSessionID   *domain.SessionID `json:"managerSessionId,omitempty"`
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
	// Usage is absent when the backend reported none. A Job whose accounting is
	// unknown must not read as a Job that cost nothing.
	Usage *UsagePayload `json:"usage,omitempty"`
}

// UsagePayload is what a Job consumed, as the backend reported it.
//
// Core stores it and shows it; it prices nothing itself. The shape is
// provider-neutral, so a timeline reads the same whichever provider produced it
// and a provider that counts differently leaves what it cannot fill at zero.
type UsagePayload struct {
	InputTokens  uint64 `json:"inputTokens"`
	OutputTokens uint64 `json:"outputTokens"`
	// Tokens served from, and written to, a prompt cache. They are the bulk of
	// a long Session and are worth telling apart from fresh input.
	CacheReadTokens  uint64  `json:"cacheReadTokens"`
	CacheWriteTokens uint64  `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd,omitempty"`
}

// usageFromProto translates reported usage, keeping "nothing reported" distinct
// from "reported as zero".
func usageFromProto(usage *backendv1.Usage) *UsagePayload {
	if usage == nil {
		return nil
	}
	return &UsagePayload{
		InputTokens:      usage.GetInputTokens(),
		OutputTokens:     usage.GetOutputTokens(),
		CacheReadTokens:  usage.GetCacheReadTokens(),
		CacheWriteTokens: usage.GetCacheWriteTokens(),
		CostUSD:          usage.GetCostUsd(),
	}
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
	ValidationID  string       `json:"validationId"`
	Approved      bool         `json:"approved"`
	ActorUserID   string       `json:"actorUserId"`
	Channel       string       `json:"channel"`
	ActorJobID    domain.JobID `json:"actorJobId,omitempty"`
	PayloadSHA256 string       `json:"payloadSha256"`
	// Title is what was decided, as the request named it ("Bash: go test"),
	// so the timeline can say what was allowed rather than that something was.
	Title string `json:"title,omitempty"`
	Note  string `json:"note,omitempty"`
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
	RequestID   string       `json:"requestId"`
	Value       string       `json:"value"`
	ActorUserID string       `json:"actorUserId"`
	Channel     string       `json:"channel"`
	ActorJobID  domain.JobID `json:"actorJobId,omitempty"`
}

// WorkspaceChangedPayload is the body of a workspace.changed event. Core keeps
// the list of files touched; the detailed diff stays backend-owned.
type WorkspaceChangedPayload struct {
	KnownDirectoryID string          `json:"knownDirectoryId,omitempty"`
	Files            []WorkspaceFile `json:"files,omitempty"`
	Additions        int32           `json:"additions"`
	Deletions        int32           `json:"deletions"`
	// Where, and between which two git trees, the diff of a file can be asked
	// of the backend. Names, never content; absent when it cannot be asked.
	Directory string `json:"directory,omitempty"`
	BaseTree  string `json:"baseTree,omitempty"`
	HeadTree  string `json:"headTree,omitempty"`
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

// SessionPinnedPayload is the body of a session.pinned event: the Session was
// pinned, or unpinned, to be reached from any Project.
type SessionPinnedPayload struct {
	Pinned bool `json:"pinned"`
}
