// Package tools defines the Core Tools exposed to agents.
//
// Core Tools are provider-independent (THREAVIA_SPEC_V1.md section 12): Core
// declares them, the backend SDK adapts them to the provider-specific tool
// mechanism, and a backend never hardcodes their names since it receives their
// specifications in the ProjectContext at Job start.
package tools

import "encoding/json"

// Name identifies a Core Tool on the wire.
type Name string

// The candidate Core Tools of specification section 12.
const (
	NameTaskCreate             Name = "task_create"
	NameTaskSearch             Name = "task_search"
	NameTaskUpdate             Name = "task_update"
	NameTaskComplete           Name = "task_complete"
	NameTaskReady              Name = "task_ready"
	NameDecisionCreate         Name = "decision_create"
	NameProjectHistorySearch   Name = "project_history_search"
	NameKnownDirectoryRegister Name = "known_directory_register"
	NameKnownDirectoryBind     Name = "known_directory_bind"
	NameWorkingDirectorySet    Name = "working_directory_set"
)

func (n Name) String() string { return string(n) }

// Spec describes one Core Tool to an agent.
type Spec struct {
	Name        Name            `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Registry resolves and invokes Core Tools.
//
// Invocations arrive over the backend control stream as CoreToolRequest and are
// answered with CoreToolResponse. The implementations land with the Tasks,
// Decisions and KnownDirectory services.
type Registry interface {
	// Specs returns the tools exposed for a Project, in the order they should be
	// presented to the agent.
	Specs(projectID string) []Spec
	// Invoke executes a tool call on behalf of a Run/Job.
	Invoke(req Request) (json.RawMessage, error)
}

// Request is one Core Tool invocation.
type Request struct {
	RequestID string
	ProjectID string
	SessionID string
	RunID     string
	JobID     string
	Name      Name
	Input     json.RawMessage
}
