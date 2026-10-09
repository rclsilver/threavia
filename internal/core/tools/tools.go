// Package tools defines the Core Tools exposed to agents.
//
// Core Tools are provider-independent (THREAVIA_SPEC_V1.md section 12): Core
// declares them, the backend SDK adapts them to the provider-specific tool
// mechanism, and a backend never hardcodes their names because it receives their
// specifications in the ProjectContext at Job start.
//
// What they exist for is in section 15: an agent that can record a decision,
// file a task or search what was done before accumulates project knowledge that
// outlives the Session, the Run and the backend it happened to run on.
package tools

import "encoding/json"

// Name identifies a Core Tool on the wire.
type Name string

// The Core Tools of specification section 12.
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
	NameArtifactPublish        Name = "artifact_publish"
)

func (n Name) String() string { return string(n) }

// Spec describes one Core Tool to an agent.
type Spec struct {
	Name        Name            `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	// FileInput names the input property that is a path on the backend
	// machine. The backend reads that file and sends its content with the call:
	// Core never reaches a backend's disk itself.
	FileInput string `json:"fileInput,omitempty"`
	// RequiresValidation marks a tool that changes where future runs execute,
	// and so what they may read without asking. The backend asks the person
	// before running it, as it would before a Write, while it allows the other
	// Core Tools on its own: an agent must not widen its own scope.
	RequiresValidation bool `json:"requiresValidation,omitempty"`
}

// object builds a JSON Schema for a tool input.
func object(properties map[string]any, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	encoded, err := json.Marshal(map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	})
	if err != nil {
		// The schemas below are literals, so this cannot fail at runtime.
		panic(err)
	}
	return encoded
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// Specs returns every Core Tool, in the order they should be presented to an
// agent: the ones it will reach for most often first.
//
// The descriptions are the only instruction an agent gets about when to use
// them, so they say what the tool is for rather than what it does.
func Specs() []Spec {
	return []Spec{
		{
			Name: NameProjectHistorySearch,
			Description: "Search everything that was already said and done in this project. " +
				"Use it before assuming something is new: the work may have been done, " +
				"discussed or rejected in an earlier session, possibly on another machine.",
			InputSchema: object(map[string]any{
				"query": str("What to look for, in natural language."),
			}, "query"),
		},
		{
			Name: NameDecisionCreate,
			Description: "Record a decision that should outlive this conversation. " +
				"Mark it IMPORTANT when every future session must know it; IMPORTANT " +
				"decisions are injected into the context of every later run. " +
				"Set supersedes when this replaces an earlier decision.",
			InputSchema: object(map[string]any{
				"title":      str("A one-line statement of the decision."),
				"content":    str("The reasoning, and what it rules out."),
				"importance": map[string]any{"type": "string", "enum": []string{"IMPORTANT", "NORMAL"}},
				"supersedes": str("Identifier of the decision this one replaces."),
			}, "title"),
		},
		{
			Name: NameTaskCreate,
			Description: "File a task so that work identified now is not lost when this " +
				"session ends. Use dependsOn to record that it waits on other tasks.",
			InputSchema: object(map[string]any{
				"title":       str("What has to be done."),
				"description": str("Any detail the next session would need."),
				"dependsOn": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Identifiers of the tasks that must be done first.",
				},
			}, "title"),
		},
		{
			Name: NameTaskReady,
			Description: "List the tasks that can be started now: those whose dependencies " +
				"are all done. Use it to choose what to work on next.",
			InputSchema: object(map[string]any{}),
		},
		{
			Name:        NameTaskSearch,
			Description: "Find tasks by text, to check whether something is already filed.",
			InputSchema: object(map[string]any{
				"query": str("What to look for."),
			}, "query"),
		},
		{
			Name:        NameTaskUpdate,
			Description: "Change a task: its title, its description, or its status.",
			InputSchema: object(map[string]any{
				"taskId":      str("Identifier of the task."),
				"title":       str("A new title, if it should change."),
				"description": str("A new description, if it should change."),
				"status":      map[string]any{"type": "string", "enum": []string{"TODO", "IN_PROGRESS", "DONE"}},
			}, "taskId"),
		},
		{
			Name:        NameTaskComplete,
			Description: "Mark a task done, which unblocks whatever depends on it.",
			InputSchema: object(map[string]any{
				"taskId": str("Identifier of the task."),
			}, "taskId"),
		},
		{
			Name:               NameKnownDirectoryRegister,
			RequiresValidation: true,
			Description: "Register a directory worth remembering across machines, and bind " +
				"it to its path here. Use it for a directory this project will come back " +
				"to, not for every directory you happen to touch.",
			InputSchema: object(map[string]any{
				"name":        str("A short logical name, such as 'puppet'."),
				"path":        str("Its absolute path on this machine."),
				"description": str("What it holds."),
				"gitRemote":   str("Its git remote, if it has one."),
			}, "name", "path"),
		},
		{
			Name:               NameKnownDirectoryBind,
			RequiresValidation: true,
			Description:        "Bind an already known directory to its path on this machine.",
			InputSchema: object(map[string]any{
				"knownDirectoryId": str("Identifier of the known directory."),
				"path":             str("Its absolute path on this machine."),
			}, "knownDirectoryId", "path"),
		},
		{
			Name:      NameArtifactPublish,
			FileInput: "path",
			Description: "Publish a file you made for the person you work with — an HTML report, " +
				"a chart, a screenshot, a diagram, an export — so that it appears in this " +
				"conversation, where they can open it, and stays with the project afterwards. " +
				"It is the only way they can see a file: they cannot reach this machine. " +
				"Write the file in your working directory or your scratch directory first, then " +
				"publish it. Use it for what is meant to be looked at, not for every file you " +
				"change: the code you edit is already shown as the job's changes.",
			InputSchema: object(map[string]any{
				"path":  str("The file to publish, absolute or relative to the working directory. Up to 10 MiB."),
				"title": str("What it is, in a few words, as the person should read it."),
			}, "path"),
		},
		{
			Name:               NameWorkingDirectorySet,
			RequiresValidation: true,
			Description: "Change the working directory of this session, durably. This is the " +
				"initial directory of future runs, not a temporary cd.",
			InputSchema: object(map[string]any{
				"knownDirectoryId": str("Identifier of the known directory to work in."),
			}, "knownDirectoryId"),
		},
	}
}
