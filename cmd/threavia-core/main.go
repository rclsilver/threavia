// Command threavia-core runs the Threavia control plane: the client-facing
// HTTP/JSON + SSE API and the backend-facing gRPC control service.
//
// Core owns platform state; backends own execution
// (THREAVIA_SPEC_V1.md section 2).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/api"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/config"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/service"
	"github.com/rclsilver/threavia/internal/core/skills"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
	"github.com/rclsilver/threavia/internal/logging"
	"github.com/rclsilver/threavia/web"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "threavia-core: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print the version and exit")
		migrateOnly = flag.Bool("migrate-only", false, "apply database migrations and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger, err := logging.New(cfg.Log.Level, cfg.Log.Format, os.Stderr)
	if err != nil {
		return err
	}
	logger = logger.With(slog.String("component", "core"), slog.String("version", version))

	if *migrateOnly {
		logger.Info("applying database migrations", slog.String("database", cfg.Postgres.Redacted()))
		return postgres.MigrateUp(cfg.Postgres, logger)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Postgres.AutoMigrate {
		logger.Info("applying database migrations", slog.String("database", cfg.Postgres.Redacted()))
		if err := postgres.MigrateUp(cfg.Postgres, logger); err != nil {
			return err
		}
	}

	db, err := postgres.Open(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("connected to postgresql", slog.String("database", cfg.Postgres.Redacted()))

	authenticator, err := auth.New(cfg.Auth)
	if err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	if authenticator.Mode() == auth.ModeNone {
		logger.Warn("authentication is disabled, every request is attributed to a single local user",
			slog.String("user", cfg.Auth.DevUserID))
	}
	if cfg.UnauthenticatedBeyondLoopback() {
		// Not refused: an operator may bind a trusted interface on purpose. But
		// whoever reaches it acts as the local user, so it must not go unnoticed.
		logger.Warn("authentication is disabled and the client API listens beyond loopback, anyone who reaches it acts as the local user",
			slog.String("addr", cfg.HTTP.Addr),
			slog.String("configure", config.EnvPrefix+"AUTH_MODE"))
	}

	registry := backendconn.NewRegistry()
	svc := service.New(postgres.NewStore(db), events.NewBroker(), registry, logger)
	svc.SetRegistration(service.RegistrationConfig{SharedKey: cfg.Backend.SharedRegistrationKey})

	if cfg.Backend.SharedRegistrationKey == "" {
		logger.Info("shared key registration is disabled, backends register with one-shot user tokens",
			slog.String("configure", config.EnvPrefix+"BACKEND_SHARED_REGISTRATION_KEY"))
	}

	// Object storage is optional at startup: without it Core runs and every
	// Artifact route refuses, which is better than refusing to start a control
	// plane because a blob store is missing.
	if cfg.S3.Enabled {
		objects, err := s3.New(ctx, cfg.S3)
		if err != nil {
			return fmt.Errorf("object storage: %w", err)
		}
		svc.SetObjectStore(objects, cfg.S3.MaxUploadBytes)
		// Skills live in object storage too: an immutable bundle with a checksum
		// is what an Artifact already is.
		svc.SetSkillAcquirer(skills.NewAcquirer(cfg.Skills))
		logger.Info("artifact storage ready",
			slog.String("endpoint", cfg.S3.Endpoint), slog.String("bucket", cfg.S3.Bucket))
	} else {
		logger.Info("artifact storage is disabled, artifact endpoints will refuse",
			slog.String("configure", config.EnvPrefix+"S3_ENABLED"))
	}

	// The service resolves the credential a backend presents on Connect.
	controlServer := backendconn.NewServer(registry, svc, svc, backendconn.Options{
		HeartbeatInterval: cfg.Backend.HeartbeatInterval,
		OfflineAfter:      cfg.Backend.OfflineAfter,
	}, logger)

	grpcServer, err := newGRPCServer(cfg.GRPC, cfg.Backend.HeartbeatInterval)
	if err != nil {
		return err
	}
	backendv1.RegisterBackendControlServer(grpcServer, controlServer)

	webUI, err := web.Handler()
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr: cfg.HTTP.Addr,
		Handler: api.NewRouter(api.Options{
			Service:       svc,
			Authenticator: authenticator,
			Database:      db,
			Version:       version,
			Logger:        logger,
			WebUI:         webUI,
		}),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		// WriteTimeout stays zero: SSE responses are long-lived by design.
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		listener, err := net.Listen("tcp", cfg.GRPC.Addr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.GRPC.Addr, err)
		}
		logger.Info("backend control service listening",
			slog.String("address", listener.Addr().String()),
			slog.Bool("tls", cfg.GRPC.TLSCertFile != ""),
		)
		if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("backend control service: %w", err)
		}
		return nil
	})

	group.Go(func() error {
		listener, err := net.Listen("tcp", cfg.HTTP.Addr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.HTTP.Addr, err)
		}
		logger.Info("client api listening", slog.String("address", listener.Addr().String()))
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("client api: %w", err)
		}
		return nil
	})

	group.Go(func() error {
		// A backend whose process wedges keeps its connection open and goes
		// silent; only a heartbeat deadline can notice.
		controlServer.Monitor(groupCtx)
		return nil
	})

	group.Go(func() error {
		// Messages planned on a Session go out from here. A run is claimed in
		// the database, so a second replica never sends the same one.
		svc.RunSchedules(groupCtx)
		return nil
	})

	group.Go(func() error {
		<-groupCtx.Done()
		logger.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(groupCtx), cfg.HTTP.ShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("client api did not shut down cleanly", slog.String("error", err.Error()))
		}

		// Ending the control streams first lets the graceful stop drain at once.
		controlServer.Shutdown()
		stopGRPC(grpcServer, cfg.GRPC.ShutdownTimeout, logger)
		return nil
	})

	if err := group.Wait(); err != nil {
		return err
	}
	logger.Info("stopped")
	return nil
}

// newGRPCServer builds the backend-facing gRPC server.
func newGRPCServer(cfg config.GRPCConfig, heartbeat time.Duration) (*grpc.Server, error) {
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(cfg.MaxRecvMsgBytes),
		// The control stream is long-lived and mostly idle; keepalive keeps it
		// alive through NAT and detects dead peers.
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    heartbeat,
			Timeout: heartbeat,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             heartbeat / 2,
			PermitWithoutStream: true,
		}),
	}

	if cfg.TLSCertFile != "" {
		creds, err := credentials.NewServerTLSFromFile(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load grpc tls material: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	}

	return grpc.NewServer(opts...), nil
}

// stopGRPC drains the control streams, then forces the server down if backends
// do not disconnect in time.
func stopGRPC(server *grpc.Server, timeout time.Duration, logger *slog.Logger) {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(timeout):
		logger.Warn("forcing backend control service shutdown", slog.Duration("after", timeout))
		server.Stop()
		<-stopped
	}
}
