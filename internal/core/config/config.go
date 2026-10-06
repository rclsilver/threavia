// Package config loads the Threavia Core configuration from the environment.
//
// Every setting required by THREAVIA_SPEC_V1.md section 29 is represented here:
// PostgreSQL, S3-compatible object storage, authentication mode and the backend
// control endpoint. Secrets are only ever read from the environment, which maps
// directly onto Kubernetes Secrets.
package config

import (
	"fmt"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
	"github.com/rclsilver/threavia/internal/envutil"
	"github.com/rclsilver/threavia/internal/logging"
)

// EnvPrefix is the common prefix of every Core environment variable.
const EnvPrefix = "THREAVIA_"

// Config is the complete Core configuration.
type Config struct {
	Log      LogConfig
	HTTP     HTTPConfig
	GRPC     GRPCConfig
	Postgres postgres.Config
	S3       s3.Config
	Auth     auth.Config
	Backend  BackendConfig
}

// LogConfig controls the structured logger.
type LogConfig struct {
	// Level is one of debug, info, warn, error.
	Level string
	// Format is text or json.
	Format string
}

// HTTPConfig is the client-facing HTTP/JSON + SSE listener (spec section 5).
type HTTPConfig struct {
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	// WriteTimeout must stay zero: SSE responses are long-lived by design.
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// GRPCConfig is the backend-facing control listener (spec section 9). Backends
// always connect outbound to this endpoint; Core never dials a backend.
type GRPCConfig struct {
	Addr string

	// TLSCertFile and TLSKeyFile enable TLS directly on the Core listener. They
	// may stay empty when TLS is terminated by an ingress.
	TLSCertFile string
	TLSKeyFile  string

	MaxRecvMsgBytes int
	ShutdownTimeout time.Duration
}

// BackendConfig holds the backend control-plane settings owned by Core.
type BackendConfig struct {
	// HeartbeatInterval is advertised to backends in the Welcome message.
	HeartbeatInterval time.Duration
	// OfflineAfter is how long Core waits without heartbeat before inferring
	// OFFLINE (spec section 7).
	OfflineAfter time.Duration

	// DevTokens maps a static bearer token to a BackendInstance id. It is a
	// development stop-gap until the registration and claim flows of
	// specification section 8 are implemented, and it is empty by default: with
	// no entry, every backend connection is rejected.
	DevTokens map[string]string

	// SharedRegistrationKey enables shared-key registration: a backend
	// registering with it creates an UNCLAIMED BackendInstance that a user then
	// claims with a one-time code (spec section 8). The registration endpoint
	// consuming it is not implemented yet.
	SharedRegistrationKey string
}

// Default returns the configuration before any environment override.
func Default() Config {
	return Config{
		Log: LogConfig{Level: "info", Format: "text"},
		HTTP: HTTPConfig{
			Addr:              ":8080",
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      0,
			IdleTimeout:       120 * time.Second,
			ShutdownTimeout:   15 * time.Second,
		},
		GRPC: GRPCConfig{
			Addr:            ":9090",
			MaxRecvMsgBytes: 16 << 20, // 16 MiB
			ShutdownTimeout: 15 * time.Second,
		},
		Postgres: postgres.DefaultConfig(),
		S3:       s3.DefaultConfig(),
		Auth: auth.Config{
			Mode:      auth.ModeNone,
			DevUserID: "dev",
		},
		Backend: BackendConfig{
			HeartbeatInterval: 15 * time.Second,
			OfflineAfter:      45 * time.Second,
		},
	}
}

// Load reads the configuration from the environment, applying defaults for every
// unset variable, and validates the result.
func Load() (Config, error) {
	cfg := Default()
	l := envutil.NewLoader(EnvPrefix)

	cfg.Log.Level = l.String("LOG_LEVEL", cfg.Log.Level)
	cfg.Log.Format = l.String("LOG_FORMAT", cfg.Log.Format)

	cfg.HTTP.Addr = l.String("HTTP_ADDR", cfg.HTTP.Addr)
	cfg.HTTP.ReadTimeout = l.Duration("HTTP_READ_TIMEOUT", cfg.HTTP.ReadTimeout)
	cfg.HTTP.ReadHeaderTimeout = l.Duration("HTTP_READ_HEADER_TIMEOUT", cfg.HTTP.ReadHeaderTimeout)
	cfg.HTTP.WriteTimeout = l.Duration("HTTP_WRITE_TIMEOUT", cfg.HTTP.WriteTimeout)
	cfg.HTTP.IdleTimeout = l.Duration("HTTP_IDLE_TIMEOUT", cfg.HTTP.IdleTimeout)
	cfg.HTTP.ShutdownTimeout = l.Duration("HTTP_SHUTDOWN_TIMEOUT", cfg.HTTP.ShutdownTimeout)

	cfg.GRPC.Addr = l.String("GRPC_ADDR", cfg.GRPC.Addr)
	cfg.GRPC.TLSCertFile = l.String("GRPC_TLS_CERT_FILE", cfg.GRPC.TLSCertFile)
	cfg.GRPC.TLSKeyFile = l.String("GRPC_TLS_KEY_FILE", cfg.GRPC.TLSKeyFile)
	cfg.GRPC.MaxRecvMsgBytes = l.Int("GRPC_MAX_RECV_MSG_BYTES", cfg.GRPC.MaxRecvMsgBytes)
	cfg.GRPC.ShutdownTimeout = l.Duration("GRPC_SHUTDOWN_TIMEOUT", cfg.GRPC.ShutdownTimeout)

	cfg.Postgres.URL = l.String("POSTGRES_URL", cfg.Postgres.URL)
	cfg.Postgres.Host = l.String("POSTGRES_HOST", cfg.Postgres.Host)
	cfg.Postgres.Port = l.Int("POSTGRES_PORT", cfg.Postgres.Port)
	cfg.Postgres.User = l.String("POSTGRES_USER", cfg.Postgres.User)
	cfg.Postgres.Password = l.String("POSTGRES_PASSWORD", cfg.Postgres.Password)
	cfg.Postgres.Database = l.String("POSTGRES_DATABASE", cfg.Postgres.Database)
	cfg.Postgres.SSLMode = l.String("POSTGRES_SSLMODE", cfg.Postgres.SSLMode)
	cfg.Postgres.MaxConns = int32(l.Int("POSTGRES_MAX_CONNS", int(cfg.Postgres.MaxConns)))
	cfg.Postgres.MinConns = int32(l.Int("POSTGRES_MIN_CONNS", int(cfg.Postgres.MinConns)))
	cfg.Postgres.ConnectTimeout = l.Duration("POSTGRES_CONNECT_TIMEOUT", cfg.Postgres.ConnectTimeout)
	cfg.Postgres.AutoMigrate = l.Bool("POSTGRES_AUTO_MIGRATE", cfg.Postgres.AutoMigrate)

	cfg.S3.Enabled = l.Bool("S3_ENABLED", cfg.S3.Enabled)
	cfg.S3.Endpoint = l.String("S3_ENDPOINT", cfg.S3.Endpoint)
	cfg.S3.Region = l.String("S3_REGION", cfg.S3.Region)
	cfg.S3.Bucket = l.String("S3_BUCKET", cfg.S3.Bucket)
	cfg.S3.AccessKeyID = l.String("S3_ACCESS_KEY_ID", cfg.S3.AccessKeyID)
	cfg.S3.SecretAccessKey = l.String("S3_SECRET_ACCESS_KEY", cfg.S3.SecretAccessKey)
	cfg.S3.UseTLS = l.Bool("S3_USE_TLS", cfg.S3.UseTLS)
	cfg.S3.ForcePathStyle = l.Bool("S3_FORCE_PATH_STYLE", cfg.S3.ForcePathStyle)
	cfg.S3.MaxUploadBytes = l.Int64("S3_MAX_UPLOAD_BYTES", cfg.S3.MaxUploadBytes)
	cfg.S3.OperationTimeout = l.Duration("S3_OPERATION_TIMEOUT", cfg.S3.OperationTimeout)

	cfg.Auth.Mode = auth.Mode(l.String("AUTH_MODE", cfg.Auth.Mode.String()))
	cfg.Auth.DevUserID = l.String("AUTH_DEV_USER_ID", cfg.Auth.DevUserID)
	cfg.Auth.BasicUsername = l.String("AUTH_BASIC_USERNAME", cfg.Auth.BasicUsername)
	cfg.Auth.BasicPassword = l.String("AUTH_BASIC_PASSWORD", cfg.Auth.BasicPassword)
	cfg.Auth.OIDCIssuer = l.String("AUTH_OIDC_ISSUER", cfg.Auth.OIDCIssuer)
	cfg.Auth.OIDCClientID = l.String("AUTH_OIDC_CLIENT_ID", cfg.Auth.OIDCClientID)
	cfg.Auth.OIDCAudience = l.String("AUTH_OIDC_AUDIENCE", cfg.Auth.OIDCAudience)

	cfg.Backend.HeartbeatInterval = l.Duration("BACKEND_HEARTBEAT_INTERVAL", cfg.Backend.HeartbeatInterval)
	cfg.Backend.OfflineAfter = l.Duration("BACKEND_OFFLINE_AFTER", cfg.Backend.OfflineAfter)
	cfg.Backend.DevTokens = l.StringMap("BACKEND_DEV_TOKENS")
	cfg.Backend.SharedRegistrationKey = l.String("BACKEND_SHARED_REGISTRATION_KEY", cfg.Backend.SharedRegistrationKey)

	if err := l.Err(); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks the loaded configuration as a whole.
func (c Config) Validate() error {
	if !logging.ValidLevel(c.Log.Level) {
		return fmt.Errorf("%sLOG_LEVEL: unknown level %q", EnvPrefix, c.Log.Level)
	}
	if !logging.ValidFormat(c.Log.Format) {
		return fmt.Errorf("%sLOG_FORMAT: unknown format %q", EnvPrefix, c.Log.Format)
	}
	if c.HTTP.Addr == "" {
		return fmt.Errorf("%sHTTP_ADDR: listen address is required", EnvPrefix)
	}
	if c.GRPC.Addr == "" {
		return fmt.Errorf("%sGRPC_ADDR: listen address is required", EnvPrefix)
	}
	if (c.GRPC.TLSCertFile == "") != (c.GRPC.TLSKeyFile == "") {
		return fmt.Errorf("%sGRPC_TLS_CERT_FILE and %sGRPC_TLS_KEY_FILE must be set together", EnvPrefix, EnvPrefix)
	}
	if c.GRPC.MaxRecvMsgBytes <= 0 {
		return fmt.Errorf("%sGRPC_MAX_RECV_MSG_BYTES: must be greater than zero", EnvPrefix)
	}
	if c.Backend.HeartbeatInterval <= 0 {
		return fmt.Errorf("%sBACKEND_HEARTBEAT_INTERVAL: must be greater than zero", EnvPrefix)
	}
	if c.Backend.OfflineAfter <= c.Backend.HeartbeatInterval {
		return fmt.Errorf("%sBACKEND_OFFLINE_AFTER: must be greater than %sBACKEND_HEARTBEAT_INTERVAL", EnvPrefix, EnvPrefix)
	}
	if err := c.Postgres.Validate(); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if err := c.S3.Validate(); err != nil {
		return fmt.Errorf("object storage: %w", err)
	}
	if err := c.Auth.Validate(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}
