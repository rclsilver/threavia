// Command threavia-backend-codex runs a Codex CLI BackendInstance.
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

	"github.com/rclsilver/threavia/internal/backends/codex/hook"
	"github.com/rclsilver/threavia/internal/backends/codex/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/logging"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--codex-policy-hook" {
		hook.Run(os.Getenv("THREAVIA_CODEX_POLICY_ENDPOINT"), os.Stdin, os.Stdout)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "threavia-backend-codex: %v\n", err)
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

	cfg, err := adapter.LoadFor("codex")
	if err != nil {
		return err
	}
	cfg.Client.BackendVersion = version

	logger, err := logging.New(cfg.Log.Level, cfg.Log.Format, os.Stderr)
	if err != nil {
		return err
	}
	logger = logger.With(
		slog.String("component", "backend-codex"),
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

	// The local tool endpoint: where Codex CLI permission prompts and agent
	// questions become Threavia requests. It is wired before it starts serving.
	tools := mcp.New(logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tools.Close(shutdownCtx)
	}()

	codex := runner.New(cfg.Provider.Binary, tools, runner.Options{
		APIKey:          os.Getenv("OPENAI_API_KEY"),
		Version:         version,
		Model:           cfg.Provider.Model,
		ReasoningEffort: cfg.Provider.ReasoningEffort,
		Scratch:         cfg.Provider.ScratchPath,
	}, logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		_ = codex.Close(shutdownCtx)
	}()
	if err := codex.Available(); err != nil {
		// Not fatal: the backend connects anyway and reports DEGRADED so the
		// user can see why it cannot work.
		logger.Warn("codex is not available", slog.String("error", err.Error()))
	}

	codexAdapter := adapter.New(cfg, codex, store, logger)
	codex.SetAsker(codexAdapter)
	tools.SetAsker(codexAdapter)
	if err := tools.Start(); err != nil {
		return err
	}

	sdk, err := client.New(cfg.Client, codexAdapter, store, logger)
	if err != nil {
		return err
	}
	codexAdapter.Bind(sdk)

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
