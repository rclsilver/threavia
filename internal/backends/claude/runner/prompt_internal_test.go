package runner

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
