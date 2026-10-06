package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// TestMessagesAreEvents pins that user and agent messages are part of the event
// timeline: there is no competing message history.
func TestMessagesAreEvents(t *testing.T) {
	t.Parallel()

	for _, messageType := range []Type{TypeUserMessage, TypeAgentMessage} {
		if !messageType.Persistent() {
			t.Errorf("%s must be a persistent event", messageType)
		}
	}
}

func TestPersistentTypes(t *testing.T) {
	t.Parallel()

	persistent := []Type{
		TypeSessionCreated, TypeSessionRenamed, TypeSessionArchived, TypeSessionRestored,
		TypeRunCreated, TypeJobCreated, TypeJobStarted, TypeJobCompleted, TypeJobFailed,
		TypeJobCancelled, TypeUserMessage, TypeAgentMessage, TypeToolStarted,
		TypeToolCompleted, TypeToolFailed, TypeValidationRequested, TypeValidationResolved,
		TypeUserInputRequested, TypeUserInputResolved, TypeTaskCreated, TypeTaskUpdated,
		TypeDecisionCreated, TypeDecisionSuperseded, TypeWorkingDirectoryChanged,
		TypeWorkspaceChanged, TypeBackendRegistered, TypeBackendRevoked,
	}
	for _, eventType := range persistent {
		if !eventType.Persistent() {
			t.Errorf("%s must be persistent", eventType)
		}
	}

	// Ephemeral signals are streamed without retention and are not event types.
	for _, ephemeral := range []Type{"heartbeat", "agent.working", "progress"} {
		if ephemeral.Persistent() {
			t.Errorf("%s is ephemeral and must not be persisted", ephemeral)
		}
	}
}

// TestEnvelopeOmitsOutOfScopeIdentifiers pins the envelope shape of
// specification section 4: scope ids are absent when the event scope does not
// include that object.
func TestEnvelopeOmitsOutOfScopeIdentifiers(t *testing.T) {
	t.Parallel()

	sessionID := domain.SessionID("11111111-1111-1111-1111-111111111111")
	envelope := Envelope{
		ID:        domain.EventID("22222222-2222-2222-2222-222222222222"),
		Sequence:  1843,
		Timestamp: time.Date(2026, 10, 6, 20, 7, 0, 0, time.UTC),
		Type:      TypeValidationRequested,
		SessionID: &sessionID,
		Payload:   json.RawMessage(`{}`),
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshalling the envelope: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding the envelope: %v", err)
	}

	for _, key := range []string{"id", "sequence", "timestamp", "type", "sessionId"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("the envelope must expose %q", key)
		}
	}
	for _, key := range []string{"projectId", "runId", "jobId"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("the envelope must omit %q when it is out of scope", key)
		}
	}
	if got := decoded["sequence"]; got != float64(1843) {
		t.Errorf("sequence = %v, want 1843", got)
	}
}
