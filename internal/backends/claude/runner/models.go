package runner

import (
	"context"
	"os/exec"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Models advertises Claude Code's stable aliases. Effort is offered only when
// the installed CLI reports support for the flag.
func (c *Claude) Models(ctx context.Context) (*backendv1.ModelCatalog, error) {
	help, err := exec.CommandContext(ctx, c.binary, "--help").Output()
	if err != nil {
		return nil, err
	}
	catalog := &backendv1.ModelCatalog{Models: []*backendv1.ModelOption{
		{Id: "sonnet", Name: "Sonnet"}, {Id: "opus", Name: "Opus"}, {Id: "haiku", Name: "Haiku"},
	}}
	if strings.Contains(string(help), "--effort") {
		catalog.Models[0].Efforts = []string{"low", "medium", "high"}
		catalog.Models[1].Efforts = []string{"low", "medium", "high"}
		for _, line := range strings.Split(string(help), "\n") {
			if strings.Contains(line, "--effort") && strings.Contains(line, "max") {
				catalog.Models[1].Efforts = append(catalog.Models[1].Efforts, "max")
			}
		}
	}
	// No asserted default: Claude may choose it from provider/account settings.
	return catalog, nil
}
