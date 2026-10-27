// Command threavia-backend-claude runs a Claude Code BackendInstance.
//
// It connects outbound to Threavia Core and keeps one long-lived bidirectional
// control stream open. Provider credentials, the native sessions and the
// filesystem stay here and never reach Core
// (THREAVIA_SPEC_V1.md sections 2 and 7).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/execution/integration"
	"github.com/rclsilver/threavia/internal/execution/worker"
	"github.com/rclsilver/threavia/internal/logging"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if handled, err := worker.Bootstrap(version); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--review-hook" {
		mcp.RunReviewHook(os.Args[2], os.Stdin, os.Stdout)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "threavia-backend-claude: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := adapter.Load()
	if err != nil {
		return err
	}
	cfg.Client.BackendVersion = version

	logger, err := logging.New(cfg.Log.Level, cfg.Log.Format, os.Stderr)
	if err != nil {
		return err
	}
	logger = logger.With(
		slog.String("component", "backend-claude"),
		slog.String("version", version),
		slog.String("instance", cfg.Client.InstanceName),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The default working directory must exist before a Job lands in it: on a
	// Kubernetes backend it sits on a fresh volume, and a missing directory
	// would fail every unscoped Run rather than the deployment.
	if err := os.MkdirAll(cfg.Provider.DefaultWorkingDirectory, 0o755); err != nil {
		return fmt.Errorf("prepare the default working directory: %w", err)
	}

	// Durable local execution state: it is what lets this backend keep working
	// through a Core outage and replay afterwards.
	store, err := state.OpenSQLite(cfg.StatePath, logger)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	logger.Info("local state opened", slog.String("path", cfg.StatePath))

	credential, err := adapter.Credential(ctx, cfg, store, logger)
	if err != nil {
		return err
	}
	cfg.Client.Token = credential

	// The local tool endpoint: where Claude Code permission prompts and agent
	// questions become Threavia requests. It is wired before it starts serving.
	tools := mcp.New(logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tools.Close(shutdownCtx)
	}()

	localClaude := runner.NewClaude(cfg.Provider.Binary, tools, runner.Options{
		Readable: cfg.Provider.DiscoveryRoots,
		Scratch:  cfg.Provider.ScratchPath,
	}, logger)
	claude, closeExecution, err := integration.Select(ctx, &cfg, localClaude, logger)
	if err != nil {
		return err
	}
	defer closeExecution()
	if err := claude.Available(); err != nil {
		// Not fatal: the backend connects anyway and reports DEGRADED so the
		// user can see why it cannot work.
		logger.Warn("claude code is not available", slog.String("error", err.Error()))
	}

	claudeAdapter := adapter.New(cfg, claude, store, logger)
	if setter, ok := claude.(interface{ SetAsker(mcp.Asker) }); ok {
		setter.SetAsker(claudeAdapter)
	}
	tools.SetAsker(claudeAdapter)
	if cfg.Execution.Mode != "kubernetes" {
		if err := tools.Start(); err != nil {
			return err
		}
	}

	probeTimeout := 15 * time.Second
	if cfg.Execution.Mode == "kubernetes" {
		probeTimeout = 2 * time.Minute
	}
	modelCtx, modelCancel := context.WithTimeout(ctx, probeTimeout)
	catalog, modelErr := claude.Models(modelCtx)
	modelCancel()
	if modelErr != nil {
		logger.Warn("model catalog unavailable", slog.String("error", modelErr.Error()))
	} else {
		cfg.Client.FeatureModelSelection = true
		cfg.Client.ModelCatalog = catalog
	}
	sdk, err := client.New(cfg.Client, claudeAdapter, store, logger)
	if err != nil {
		return err
	}
	claudeAdapter.Bind(sdk)

	logger.Info("starting",
		slog.String("coreAddress", cfg.Client.CoreAddress),
		slog.Bool("tls", cfg.Client.TLS.Enabled),
		slog.Int("maxConcurrentRuns", int(cfg.Client.MaxConcurrentRuns)),
		slog.Any("discoveryRoots", cfg.Provider.DiscoveryRoots),
	)

	if err := sdk.Run(ctx); err != nil {
		return err
	}
	logger.Info("stopped")
	return nil
}
