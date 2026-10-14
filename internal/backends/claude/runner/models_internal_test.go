package runner

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
)

func TestClaudeCatalogReadsWrappedEffortOptions(t *testing.T) {
	for _, test := range []struct {
		name, help string
		want       []string
	}{
		{"unsupported", "  --model <model> Choose a model\n", nil},
		{"one line", "  --effort <level> (low, medium, high, max)\n  --max-actions <n> Limit\n", []string{"low", "medium", "high", "max"}},
		{"wrapped", "  --effort <level> Effort level for the current session\n                    (low, medium, high, xhigh, max)\n  --environment <id> Create a session\n", []string{"low", "medium", "high", "xhigh", "max"}},
		{"another flag", "  --effort <level> (low, medium, high)\n  --limit <n> Maximum, max or xhigh\n", []string{"low", "medium", "high"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := claudeModelCatalog(test.help)
			if !slices.Equal(catalog.Models[1].Efforts, test.want) {
				t.Fatalf("Opus efforts = %v, want %v", catalog.Models[1].Efforts, test.want)
			}
		})
	}
}

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
