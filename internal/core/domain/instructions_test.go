package domain

import (
	"strings"
	"testing"
)

// TestLayeredEmpty pins that nothing to say produces nothing at all, so a
// backend that prefixes the supervision skips it as it always has.
func TestLayeredEmpty(t *testing.T) {
	t.Parallel()

	if got := Layered(LayeredPreamble, nil); got != "" {
		t.Errorf("Layered(nil) = %q, want empty", got)
	}
	if got := Layered(LayeredPreamble, []Layer{{"Global", "  "}}); got != "" {
		t.Errorf("Layered(blank layer) = %q, want empty", got)
	}
}

// TestLayeredSingleIsVerbatim pins that a lone level carries no header and no
// preamble: there is nothing to rank it against.
func TestLayeredSingleIsVerbatim(t *testing.T) {
	t.Parallel()

	got := Layered(LayeredPreamble, []Layer{{"Global", ""}, {"Project p", " Use worktrees. "}})
	if got != "Use worktrees." {
		t.Errorf("Layered(single) = %q, want %q", got, "Use worktrees.")
	}
}

func TestLayeredOrderAndPreamble(t *testing.T) {
	t.Parallel()

	got := Layered(LayeredPreamble, []Layer{{"Global", "A"}, {"Project p", "B"}, {"Backend z", "C"}})
	want := LayeredPreamble + "\n\n[Global]\nA\n\n[Project p]\nB\n\n[Backend z]\nC"
	if got != want {
		t.Errorf("Layered(three) =\n%s\nwant\n%s", got, want)
	}
}

func TestInstructionsValidate(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 4001)
	if err := (Instructions{Agent: long}).Validate(); err == nil {
		t.Error("an agent text of 4001 characters was accepted")
	}
	if err := (Instructions{Supervision: long}).Validate(); err == nil {
		t.Error("a supervision text of 4001 characters was accepted")
	}
	limit := strings.Repeat("a", 4000)
	if err := (Instructions{Agent: limit, Supervision: limit}).Validate(); err != nil {
		t.Errorf("texts of 4000 characters were refused: %v", err)
	}
}

// TestLayeredAgentEndsStackedTextWithANewline pins the newline a stacked agent
// text ends with, and that a single layer is still sent as written.
func TestLayeredAgentEndsStackedTextWithANewline(t *testing.T) {
	t.Parallel()

	if got, want := LayeredAgent([]Layer{{"Global", "A"}, {"Backend z", "C"}}), LayeredPreamble+"\n\n[Global]\nA\n\n[Backend z]\nC\n"; got != want {
		t.Errorf("LayeredAgent(two) = %q, want %q", got, want)
	}
	if got := LayeredAgent([]Layer{{"Global", " A "}}); got != "A" {
		t.Errorf("LayeredAgent(single) = %q, want %q", got, "A")
	}
	if got := LayeredAgent(nil); got != "" {
		t.Errorf("LayeredAgent(nil) = %q, want empty", got)
	}
}
