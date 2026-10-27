// Package protocol is the versioned, execution-scoped worker wire contract.
package protocol

import (
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

const Version = 1

type Tool struct {
	Name, Description string
	InputSchema       map[string]any
	RawResult         bool
}
type Start struct {
	Version int
	Params  runner.StartParams
	Tools   []Tool
}
type Ask struct {
	Name, Prompt, FileInput string
	Input                   map[string]any
	Choices                 []string
	FreeText                bool
}
type Event struct {
	Kind, Text, CallID, Name, Code, NativeSessionID, Model, Effort string
	Input, Output                                                  map[string]any
	Workspace                                                      workspace.Summary
	Usage                                                          *backendv1.Usage
	Block                                                          *runner.ProviderBlock
	Quotas                                                         quotas.Status
	Complete                                                       bool
}
type Input struct {
	Text      string
	Interrupt bool
}
type Policy = policy.Policy
type Decision = mcp.Decision
type Status struct {
	Available                bool
	Error                    string
	Authenticated, AuthKnown bool
	Models                   *backendv1.ModelCatalog
	ModelError               string
	Quotas                   quotas.Status
}
