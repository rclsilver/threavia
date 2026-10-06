// Command backend-example is the smallest possible BackendInstance built on the
// Threavia backend SDK.
//
// It connects to Core, completes the Hello/Welcome handshake, reports itself
// READY and heartbeats, but accepts no Job: it exists to show the SDK surface
// and to exercise the control protocol end to end.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// echoHandler accepts the connection and rejects every command, which is what
// embedding client.BaseHandler gives for free.
type echoHandler struct {
	client.BaseHandler

	sdk    *client.Client
	logger *slog.Logger
}

func (h *echoHandler) OnConnected(ctx context.Context, welcome *backendv1.Welcome) error {
	h.logger.Info("connected", slog.String("connectionId", welcome.GetConnectionId()))
	return h.sdk.SendStatus(ctx,
		backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY,
		backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED,
	)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := client.DefaultConfig()
	cfg.CoreAddress = envOr("THREAVIA_BACKEND_CORE_ADDRESS", "localhost:9090")
	cfg.Token = os.Getenv("THREAVIA_BACKEND_TOKEN")
	cfg.InstanceName = envOr("THREAVIA_BACKEND_INSTANCE_NAME", "backend-example")
	cfg.BackendName = "example"
	cfg.BackendVersion = "dev"
	// Local development Core: remote access must use TLS.
	cfg.TLS.Enabled = false

	handler := &echoHandler{logger: logger}

	sdk, err := client.New(cfg, handler, state.NewMemoryStore(), logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend-example: %v\n", err)
		os.Exit(1)
	}
	handler.sdk = sdk

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := sdk.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "backend-example: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
