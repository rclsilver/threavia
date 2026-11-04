// Command threavia-backend-ollama runs a BackendInstance whose agent is Codex
// CLI and whose models are served by Ollama.
//
// Ollama serves models, not agents: Codex CLI is the agent, with Ollama as its
// model provider, so everything the Codex backend does with hooks, approvals,
// sandbox and tools applies unchanged. What differs is the provider around it:
// no account to authenticate, no quota, and a model catalog read from the
// Ollama server rather than from OpenAI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rclsilver/threavia/internal/backends/codex/hook"
	"github.com/rclsilver/threavia/internal/backends/codex/runner"
	"github.com/rclsilver/threavia/internal/backends/ollama"
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
		fmt.Fprintf(os.Stderr, "threavia-backend-ollama: %v\n", err)
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

	cfg, err := adapter.LoadFor("ollama")
	if err != nil {
		return err
	}
	cfg.Client.BackendVersion = version
	// The Kubernetes executor starts Codex in a worker pod that knows nothing
	// of Ollama yet; refusing is better than running Jobs against OpenAI.
	if cfg.Execution.Mode == "kubernetes" {
		return errors.New("THREAVIA_BACKEND_EXECUTION_MODE: the ollama backend runs locally only")
	}
	server, err := ollama.New(cfg.Provider.OllamaURL)
	if err != nil {
		return fmt.Errorf("THREAVIA_BACKEND_OLLAMA_URL: %w", err)
	}

	logger, err := logging.New(cfg.Log.Level, cfg.Log.Format, os.Stderr)
	if err != nil {
		return err
	}
	logger = logger.With(
		slog.String("component", "backend-ollama"),
		slog.String("version", version),
		slog.String("instance", cfg.Client.InstanceName),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.Provider.DefaultWorkingDirectory, 0o755); err != nil {
		return fmt.Errorf("prepare the default working directory: %w", err)
	}

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

	tools := mcp.New(logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tools.Close(shutdownCtx)
	}()

	// Codex keeps its conversations and configuration in a home of its own,
	// apart from a Codex backend or a person's ~/.codex on the same account:
	// ChatGPT credentials there would send Codex to OpenAI for its catalog.
	if os.Getenv("CODEX_HOME") == "" {
		home := filepath.Join(filepath.Dir(cfg.StatePath), "codex-home")
		if err := os.MkdirAll(home, 0o700); err != nil {
			return fmt.Errorf("prepare CODEX_HOME: %w", err)
		}
		if err := os.Setenv("CODEX_HOME", home); err != nil {
			return err
		}
	}

	codex := runner.New(cfg.Provider.Binary, tools, runner.Options{
		Version:         version,
		Model:           cfg.Provider.Model,
		ReasoningEffort: cfg.Provider.ReasoningEffort,
		Scratch:         cfg.Provider.ScratchPath,
		Ollama:          server,
		CatalogPath:     filepath.Join(filepath.Dir(cfg.StatePath), "models.json"),
		ContextLength:   cfg.Provider.OllamaContextLength,
	}, logger)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		_ = codex.Close(shutdownCtx)
	}()
	if err := codex.Available(); err != nil {
		// Not fatal: the backend connects anyway and reports DEGRADED so the
		// user can see why it cannot work.
		logger.Warn("ollama backend is not available", slog.String("error", err.Error()))
	}

	ollamaAdapter := adapter.New(cfg, codex, store, logger)
	codex.SetAsker(ollamaAdapter)
	tools.SetAsker(ollamaAdapter)
	if err := tools.Start(); err != nil {
		return err
	}

	// The models are Ollama's, read again at every status report: a server
	// down now reports them once it is back, and a model pulled later shows
	// up without a restart. Until then Core refuses any explicit model.
	cfg.Client.FeatureModelSelection = true
	modelCtx, modelCancel := context.WithTimeout(ctx, 15*time.Second)
	catalog, modelErr := codex.Models(modelCtx)
	modelCancel()
	if modelErr != nil {
		logger.Warn("model catalog unavailable", slog.String("error", modelErr.Error()))
	} else {
		cfg.Client.ModelCatalog = catalog
	}
	sdk, err := client.New(cfg.Client, ollamaAdapter, store, logger)
	if err != nil {
		return err
	}
	ollamaAdapter.Bind(sdk)

	logger.Info("starting",
		slog.String("coreAddress", cfg.Client.CoreAddress),
		slog.String("ollama", server.URL()),
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
