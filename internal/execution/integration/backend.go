// Package integration selects the backend execution runtime. Local execution
// remains the default on Linux and macOS; Kubernetes mode never probes or runs
// a native provider in the trusted control backend process.
package integration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/execution/client"
	"github.com/rclsilver/threavia/internal/execution/remote"
)

type Provider interface {
	runner.Runner
	Models(context.Context) (*backendv1.ModelCatalog, error)
}

func Select(ctx context.Context, cfg *adapter.Config, local Provider, logger *slog.Logger) (Provider, func(), error) {
	if cfg.Execution.Mode != "kubernetes" {
		return local, func() {}, nil
	}
	// Prepare shared subpaths as the backend user before a kubelet can create
	// them as root for a read-only worker mount (including the first probe).
	for _, dir := range []string{filepath.Join(cfg.Provider.SkillCachePath, "bundles"), filepath.Join(cfg.Provider.SkillCachePath, "jobs"), cfg.Provider.ScratchPath} {
		if dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, nil, fmt.Errorf("prepare shared execution data: %w", err)
			}
		}
	}
	c, err := client.New(ctx, client.Config{Endpoint: cfg.Execution.Endpoint, CAFile: cfg.Execution.CAFile, TokenFile: cfg.Execution.TokenFile, WorkingDirectory: cfg.Provider.DefaultWorkingDirectory})
	if err != nil {
		return nil, nil, err
	}
	cfg.MonitorExecutor = c
	cfg.Client.FeaturePersistentMonitors = true
	return remote.New(c, logger), func() { _ = c.Close() }, nil
}
