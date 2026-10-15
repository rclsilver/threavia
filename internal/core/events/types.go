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
	TypeSessionCreated      Type = "session.created"
	TypeSessionRenamed      Type = "session.renamed"
	TypeSessionArchived     Type = "session.archived"
	TypeSessionRestored     Type = "session.restored"
	TypeSessionDeleted      Type = "session.deleted"
	TypeSessionPinned       Type = "session.pinned"
	TypeSessionModelChanged Type = "session.model_changed"

	TypeRunCreated Type = "run.created"

	TypeJobCreated        Type = "job.created"
	TypeJobStarted        Type = "job.started"
	TypeJobCompleted      Type = "job.completed"
	TypeJobFailed         Type = "job.failed"
	TypeJobCancelled      Type = "job.cancelled"
	TypeJobWaitingBackend Type = "job.waiting_backend"

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
	TypeTaskDeleted        Type = "task.deleted"
	TypeDecisionCreated    Type = "decision.created"
	TypeDecisionSuperseded Type = "decision.superseded"
	TypeDecisionUpdated    Type = "decision.updated"
	TypeDecisionDeleted    Type = "decision.deleted"

	TypeWorkingDirectoryChanged Type = "working_directory.changed"
	TypeWorkspaceChanged        Type = "workspace.changed"
	TypeArtifactCreated         Type = "artifact.created"
	TypeArtifactDeleted         Type = "artifact.deleted"
	TypeSkillInstalled          Type = "skill.installed"
	TypeSkillRemoved            Type = "skill.removed"

	TypeBackendRegistered     Type = "backend.registered"
	TypeBackendStatusChanged  Type = "backend.status_changed"
	TypeBackendRevoked        Type = "backend.revoked"
	TypeBackendQuotasUpdated  Type = "backend.quotas_updated"
	TypeBackendModelsUpdated  Type = "backend.models_updated"
	TypeProjectUpdated        Type = "project.updated"
	TypeProjectCreated        Type = "project.created"
	TypeProjectArchived       Type = "project.archived"
	TypeProjectRestored       Type = "project.restored"
	TypeProjectDeleted        Type = "project.deleted"
	TypeDirectoryChanged      Type = "directory.changed"
	TypeBackendDefaultChanged Type = "backend.default_changed"

	// TypeScheduleSkipped says, in the Session, that a scheduled message was
	// not sent and why.
	TypeScheduleSkipped Type = "schedule.skipped"
	TypeScheduleCreated Type = "schedule.created"
	TypeScheduleUpdated Type = "schedule.updated"
	TypeScheduleDeleted Type = "schedule.deleted"
)

// persistentTypes is the set of event types Core persists and assigns a global
// sequence to.
var persistentTypes = map[Type]bool{
	TypeProjectCreated:          true,
	TypeProjectArchived:         true,
	TypeProjectRestored:         true,
	TypeScheduleCreated:         true,
	TypeScheduleUpdated:         true,
	TypeScheduleDeleted:         true,
	TypeBackendDefaultChanged:   true,
	TypeDirectoryChanged:        true,
	TypeSessionCreated:          true,
	TypeSessionRenamed:          true,
	TypeSessionArchived:         true,
	TypeSessionRestored:         true,
	TypeSessionDeleted:          true,
	TypeSessionPinned:           true,
	TypeSessionModelChanged:     true,
	TypeRunCreated:              true,
	TypeJobCreated:              true,
	TypeJobStarted:              true,
	TypeJobCompleted:            true,
	TypeJobFailed:               true,
	TypeJobCancelled:            true,
	TypeJobWaitingBackend:       true,
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
	TypeTaskDeleted:             true,
	TypeDecisionCreated:         true,
	TypeDecisionSuperseded:      true,
	TypeDecisionUpdated:         true,
	TypeDecisionDeleted:         true,
	TypeWorkingDirectoryChanged: true,
	TypeWorkspaceChanged:        true,
	TypeArtifactCreated:         true,
	TypeArtifactDeleted:         true,
	TypeSkillInstalled:          true,
	TypeSkillRemoved:            true,
	TypeBackendRegistered:       true,
	TypeBackendStatusChanged:    true,
	TypeBackendRevoked:          true,
	TypeBackendQuotasUpdated:    true,
	TypeBackendModelsUpdated:    true,
	TypeProjectUpdated:          true,
	TypeProjectDeleted:          true,
	TypeScheduleSkipped:         true,
}

func (t Type) String() string { return string(t) }

// Persistent reports whether t is a known persistent event type. Ephemeral
// signals such as heartbeats, transient progress or "agent working" are streamed
// without permanent retention and are not Types.
func (t Type) Persistent() bool { return persistentTypes[t] }
