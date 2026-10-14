package runner

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
)

func TestSessionModelsArePassedOnFreshAndResumedCommands(t *testing.T) {
	c := NewClaude("claude", nil, Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, resume := range []bool{false, true} {
		cmd := c.command(context.Background(), StartParams{Model: "opus", Effort: "max"}, "", "native", resume, "tools.json")
		for _, pair := range [][2]string{{"--model", "opus"}, {"--effort", "max"}} {
			i := slices.Index(cmd.Args, pair[0])
			if i < 0 || i+1 >= len(cmd.Args) || cmd.Args[i+1] != pair[1] {
				t.Fatalf("missing %v: %v", pair, cmd.Args)
			}
		}
	}
	next := c.command(context.Background(), StartParams{}, "", "native", true, "tools.json")
	if slices.Contains(next.Args, "--model") || slices.Contains(next.Args, "--effort") {
		t.Fatal("one session's settings leaked into another")
	}
}
