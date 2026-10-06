// Package events defines the Core observable event model: the persistent event
// types, the envelope exposed to clients and the global sequence cursor
// (THREAVIA_SPEC_V1.md sections 3.6 and 4).
//
// Messages are events too: there is no competing message-history system.
package events

// Type is a persistent observable event type.
type Type string

// Persistent event types. Core state is stored directly in relational tables:
// this log is the observable timeline and the realtime synchronization
// mechanism, not an event-sourcing journal to replay.
const (
	TypeSessionCreated  Type = "session.created"
	TypeSessionRenamed  Type = "session.renamed"
	TypeSessionArchived Type = "session.archived"
	TypeSessionRestored Type = "session.restored"

	TypeRunCreated Type = "run.created"

	TypeJobCreated   Type = "job.created"
	TypeJobStarted   Type = "job.started"
	TypeJobCompleted Type = "job.completed"
	TypeJobFailed    Type = "job.failed"
	TypeJobCancelled Type = "job.cancelled"

	TypeUserMessage  Type = "user.message"
	TypeAgentMessage Type = "agent.message"

	TypeToolStarted   Type = "tool.started"
	TypeToolCompleted Type = "tool.completed"
	TypeToolFailed    Type = "tool.failed"

	TypeValidationRequested Type = "validation.requested"
	TypeValidationResolved  Type = "validation.resolved"
	TypeUserInputRequested  Type = "user_input.requested"
	TypeUserInputResolved   Type = "user_input.resolved"

	TypeTaskCreated        Type = "task.created"
	TypeTaskUpdated        Type = "task.updated"
	TypeDecisionCreated    Type = "decision.created"
	TypeDecisionSuperseded Type = "decision.superseded"

	TypeWorkingDirectoryChanged Type = "working_directory.changed"
	TypeWorkspaceChanged        Type = "workspace.changed"

	TypeBackendRegistered Type = "backend.registered"
	TypeBackendRevoked    Type = "backend.revoked"
)

// persistentTypes is the set of event types Core persists and assigns a global
// sequence to.
var persistentTypes = map[Type]bool{
	TypeSessionCreated:          true,
	TypeSessionRenamed:          true,
	TypeSessionArchived:         true,
	TypeSessionRestored:         true,
	TypeRunCreated:              true,
	TypeJobCreated:              true,
	TypeJobStarted:              true,
	TypeJobCompleted:            true,
	TypeJobFailed:               true,
	TypeJobCancelled:            true,
	TypeUserMessage:             true,
	TypeAgentMessage:            true,
	TypeToolStarted:             true,
	TypeToolCompleted:           true,
	TypeToolFailed:              true,
	TypeValidationRequested:     true,
	TypeValidationResolved:      true,
	TypeUserInputRequested:      true,
	TypeUserInputResolved:       true,
	TypeTaskCreated:             true,
	TypeTaskUpdated:             true,
	TypeDecisionCreated:         true,
	TypeDecisionSuperseded:      true,
	TypeWorkingDirectoryChanged: true,
	TypeWorkspaceChanged:        true,
	TypeBackendRegistered:       true,
	TypeBackendRevoked:          true,
}

func (t Type) String() string { return string(t) }

// Persistent reports whether t is a known persistent event type. Ephemeral
// signals such as heartbeats, transient progress or "agent working" are streamed
// without permanent retention and are not Types.
func (t Type) Persistent() bool { return persistentTypes[t] }
