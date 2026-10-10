package runner

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
)

// TestThePromptNamesWhatIsOnPath pins a line that pays for itself.
//
// With a short service PATH the agent could not reach sed, rg or jq, so it
// prefixed every command with an assignment to PATH. No permission rule can
// approve such a command — the provider refuses to decide one whose value it
// cannot resolve before running it — so that one habit turned every reading
// command into a question for someone who might be asleep.
//
// Naming the tools rather than promising them is the part that matters: a
// claim the agent cannot check is a claim it has every reason to doubt, and
// doubt is what produced the prefix.
func TestThePromptNamesWhatIsOnPath(t *testing.T) {
	t.Parallel()

	prompt := systemPrompt(StartParams{}, []string{"tofu", "kubectl"}, "/var/lib/threavia/scratch/r1")
	for _, want := range []string{"PATH", "approval", "tofu", "kubectl"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt never mentions %q:\n%s", want, prompt)
		}
	}
}

// TestWhatIsOnPathIsRead pins that the list comes from PATH rather than from a
// list someone keeps up to date, which is what makes adding a package to the
// service the whole of adding it for the agent.
func TestWhatIsOnPathIsRead(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if found := executablesOnPath(); len(found) != 0 {
		t.Fatalf("an empty PATH produced %v", found)
	}

	dir := t.TempDir()
	for _, name := range []string{"tofu", "kubectl", "age"} {
		if err := writeExecutable(t, dir, name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	found := executablesOnPath()
	if len(found) != 3 || found[0] != "age" || found[1] != "kubectl" || found[2] != "tofu" {
		t.Fatalf("found %v, want age kubectl tofu in order", found)
	}
}

func writeExecutable(t *testing.T, dir, name string) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755)
}

// TestTheRunHasSomewhereToWrite pins the directory that replaces /tmp.
//
// Without one the agent writes to /tmp and then has to be approved to read
// back what it wrote a second earlier: a file outside the working directory is
// a prompt, and /tmp is outside every working directory. One per Run, not per
// Job, because the agent writes in answer to one message and reads in answer
// to the next.
func TestTheRunHasSomewhereToWrite(t *testing.T) {
	root := t.TempDir()
	c := NewClaude("claude", nil, Options{Scratch: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	first := c.scratchFor("run-1")
	if first == "" {
		t.Fatal("a run with a scratch root got no directory")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("the directory was not made: %v", err)
	}
	// The same Run comes back to the same place, which is the whole point.
	if again := c.scratchFor("run-1"); again != first {
		t.Fatalf("the run moved from %q to %q between two jobs", first, again)
	}
	if other := c.scratchFor("run-2"); other == first {
		t.Fatal("two runs share a directory")
	}

	// It is named where the agent will read it.
	if prompt := systemPrompt(StartParams{}, nil, first); !strings.Contains(prompt, first) {
		t.Errorf("the prompt never names the scratch directory:\n%s", prompt)
	}

	// And a backend configured without one says nothing rather than pointing
	// the agent at a directory that is not there.
	bare := NewClaude("claude", nil, Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if bare.scratchFor("run-1") != "" {
		t.Error("a backend with no scratch root invented one")
	}
}

// TestTheReviewerIsToldBeforeEveryMessage pins where a supervision statement
// goes, and why it is not in the system prompt with everything else.
//
// The reviewer reads the user's messages and the repository's own instruction
// file. It does not read the system prompt a backend appends — which is how
// the Project instructions became invisible to it. So the statement travels in
// the message, as something the user said, and in front of every one of them:
// the reviewer re-reads the conversation on each check, and a long session
// loses its oldest messages.
func TestTheReviewerIsToldBeforeEveryMessage(t *testing.T) {
	t.Parallel()

	supervised := StartParams{
		Prompt: "Déploie la release",
		Policy: policy.Policy{
			Mode:        backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED,
			Supervision: "This cluster is shared. Never deploy without asking.",
		},
	}
	sent := withSupervision(supervised)
	if !strings.Contains(sent, "Never deploy without asking") {
		t.Fatalf("the reviewer is never told:\n%s", sent)
	}
	if !strings.HasSuffix(sent, "Déploie la release") {
		t.Fatalf("the message was not kept whole at the end:\n%s", sent)
	}
	// And it is not quietly also in the system prompt, where nobody would read
	// it and where it would spend the agent's context twice.
	if prompt := systemPrompt(supervised, nil, ""); strings.Contains(prompt, "Never deploy") {
		t.Error("the statement is in the system prompt, which the reviewer does not read")
	}

	// The other modes have no reviewer to address, so the message is the
	// message.
	for _, mode := range []backendv1.ExecutionMode{
		backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE,
		backendv1.ExecutionMode_EXECUTION_MODE_GUARDED,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
	} {
		params := supervised
		params.Policy.Mode = mode
		if sent := withSupervision(params); sent != params.Prompt {
			t.Errorf("%v says something to a reviewer it does not have:\n%s", mode, sent)
		}
	}

	// Nothing to say, nothing added.
	bare := supervised
	bare.Policy.Supervision = "   "
	if sent := withSupervision(bare); sent != bare.Prompt {
		t.Errorf("an empty statement still prefixed the message:\n%s", sent)
	}
}

// TestThePromptSaysHowToShowAFile names the tool that carries a file to the
// person — found by what it declares — and stays silent when there is none.
func TestThePromptSaysHowToShowAFile(t *testing.T) {
	with := StartParams{CoreTools: []mcp.CoreTool{{Name: "task_create"}, {Name: "artifact_publish", FileInput: "path"}}}
	prompt := systemPrompt(with, nil, "")
	if !strings.Contains(prompt, "mcp__"+mcp.ServerName+"__artifact_publish") {
		t.Fatalf("the prompt must name the tool that publishes a file:\n%s", prompt)
	}
	// As with schedules: Threavia's way, and nothing else.
	if !strings.Contains(prompt, "only way to show them") || !strings.Contains(prompt, "web server") {
		t.Fatalf("the prompt must rule out the other ways of showing a file:\n%s", prompt)
	}
	without := StartParams{CoreTools: []mcp.CoreTool{{Name: "task_create"}}}
	if prompt := systemPrompt(without, nil, ""); strings.Contains(prompt, "publish") {
		t.Fatalf("no file tool, nothing to say about publishing:\n%s", prompt)
	}
}
