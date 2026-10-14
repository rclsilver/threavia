package runner

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"unicode"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Models advertises Claude Code's stable aliases. Effort is offered only when
// the installed CLI reports support for the flag.
func (c *Claude) Models(ctx context.Context) (*backendv1.ModelCatalog, error) {
	help, err := exec.CommandContext(ctx, c.binary, "--help").Output()
	if err != nil {
		return nil, err
	}
	return claudeModelCatalog(string(help)), nil
}

func claudeModelCatalog(help string) *backendv1.ModelCatalog {
	catalog := &backendv1.ModelCatalog{Models: []*backendv1.ModelOption{
		{Id: "sonnet", Name: "Sonnet"}, {Id: "opus", Name: "Opus"}, {Id: "haiku", Name: "Haiku"},
	}}
	var effortHelp strings.Builder
	readingEffort := false
	for _, line := range strings.Split(help, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--effort ") {
			readingEffort = true
		} else if readingEffort && strings.HasPrefix(line, "-") {
			break
		}
		if readingEffort {
			effortHelp.WriteString(line + " ")
		}
	}
	if readingEffort {
		catalog.Models[0].Efforts = []string{"low", "medium", "high"}
		catalog.Models[1].Efforts = []string{"low", "medium", "high"}
		values := strings.FieldsFunc(effortHelp.String(), func(r rune) bool { return !unicode.IsLetter(r) })
		for _, effort := range []string{"xhigh", "max"} {
			if slices.Contains(values, effort) {
				catalog.Models[1].Efforts = append(catalog.Models[1].Efforts, effort)
			}
		}
	}
	// No asserted default: Claude may choose it from provider/account settings.
	return catalog
}
