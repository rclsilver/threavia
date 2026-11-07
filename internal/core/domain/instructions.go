package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxInstructionsLength bounds each instructions text, in characters (runes).
// It takes the figure of the supervision statement of a policy, which counts
// bytes instead, so any text that passed that check also passes this one.
const MaxInstructionsLength = 4000

// Instructions are what one level — global, project or backend — tells the
// agent and its reviewer.
//
// The two texts have different readers, for the reasons given on
// ExecutionPolicy.Supervision: the agent reads its system prompt, the reviewer
// of SUPERVISED reads only what the user said. They live side by side because
// they are written at the same levels and edited in the same place.
type Instructions struct {
	Agent       string `json:"agent"`
	Supervision string `json:"supervision"`
}

// Validate checks the length of both texts.
func (i Instructions) Validate() error {
	if utf8.RuneCountInString(i.Agent) > MaxInstructionsLength {
		return fmt.Errorf("the agent instructions are too long (at most %d characters)", MaxInstructionsLength)
	}
	if utf8.RuneCountInString(i.Supervision) > MaxInstructionsLength {
		return fmt.Errorf("the supervision instructions are too long (at most %d characters)", MaxInstructionsLength)
	}
	return nil
}

// Layer is one level's contribution to an assembled text.
type Layer struct {
	// Label names the level in the assembled text: "Global", "Project <name>",
	// "Backend <name>", "Session", "Job".
	Label string
	Text  string
}

// LayeredPreamble opens a text assembled from more than one level.
//
// Each level only adds to the ones above it, so a correction is written as a
// further instruction. The reader has to be told which one wins, or it will
// pick whichever it read last or found most emphatic. The last sentence covers
// what the backend appends after Core's text, such as LOCAL_INSTRUCTIONS.
const LayeredPreamble = "These instructions come from several levels, broadest first. " +
	"When two of them conflict, the later (narrower) level wins. " +
	"Anything appended after them is narrower still."

// Layered assembles the layers, given broadest first, into one text.
//
// Blank layers are dropped. A single remaining layer is returned as it is,
// without a header or the preamble: nothing is stacked, and a header would be
// noise in every installation that only ever writes one level. With two or
// more, each gets a labelled section under the preamble.
func Layered(layers []Layer) string {
	kept := make([]Layer, 0, len(layers))
	for _, layer := range layers {
		if text := strings.TrimSpace(layer.Text); text != "" {
			kept = append(kept, Layer{Label: layer.Label, Text: text})
		}
	}
	switch len(kept) {
	case 0:
		return ""
	case 1:
		return kept[0].Text
	}
	sections := make([]string, 0, len(kept)+1)
	sections = append(sections, LayeredPreamble)
	for _, layer := range kept {
		sections = append(sections, "["+layer.Label+"]\n"+layer.Text)
	}
	return strings.Join(sections, "\n\n")
}
