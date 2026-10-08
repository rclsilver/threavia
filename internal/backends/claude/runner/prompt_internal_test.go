package runner

import (
	"strings"
	"testing"
)

// TestThePromptSaysThePathIsSet pins a line that pays for itself.
//
// With a short service PATH the agent could not reach sed, rg or jq, so it
// prefixed every command with an assignment to PATH. Claude Code asks for
// approval on any command that writes a special shell variable, whatever else
// the command does — so that one habit turned every reading command into a
// question for someone who might be asleep. The PATH is fixed; this is what
// stops the habit, and it is invisible enough to lose in a refactor.
func TestThePromptSaysThePathIsSet(t *testing.T) {
	t.Parallel()

	prompt := systemPrompt(StartParams{})
	for _, want := range []string{"PATH", "approval"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt never mentions %q:\n%s", want, prompt)
		}
	}
}
