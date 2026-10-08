package runner

import (
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

	prompt := systemPrompt(StartParams{}, []string{"tofu", "kubectl"})
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
