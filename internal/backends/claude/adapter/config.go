// Package adapter translates the provider-independent Threavia backend protocol
// into Claude Code operations, and back.
//
// Keeping this translation isolated is what lets Core stay free of any
// provider-specific concept (THREAVIA_SPEC_V1.md section 25).
package adapter

import (
	"errors"
	"os"
	"path/filepath"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/envutil"
	"github.com/rclsilver/threavia/internal/logging"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
)

// EnvPrefix is the common prefix of every Claude backend environment variable.
const EnvPrefix = "THREAVIA_BACKEND_"

// LogConfig controls the backend logger.
type LogConfig struct {
	Level  string
	Format string
}

// ClaudeConfig holds the provider-specific settings.
type ClaudeConfig struct {
	// Binary is the Claude Code executable.
	Binary string
	// DiscoveryRoots constrain automatic directory discovery and search only.
	// They are explicitly not a security sandbox: filesystem access remains real
	// OS behaviour (spec sections 11 and 28).
	DiscoveryRoots []string
}

// Config is the complete Claude backend configuration.
type Config struct {
	Log    LogConfig
	Client client.Config
	Claude ClaudeConfig

	// StatePath is where the durable local execution state lives
	// (spec section 10).
	StatePath string
}

// Default returns the configuration before any environment override.
func Default() Config {
	clientCfg := client.DefaultConfig()
	clientCfg.BackendName = "claude"
	clientCfg.CoreAddress = "localhost:9090"
	clientCfg.InstanceName = defaultInstanceName()

	return Config{
		Log:       LogConfig{Level: logging.LevelInfo, Format: logging.FormatText},
		Client:    clientCfg,
		Claude:    ClaudeConfig{Binary: "claude"},
		StatePath: defaultStatePath(),
	}
}

// Load reads the configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Default()
	l := envutil.NewLoader(EnvPrefix)

	cfg.Log.Level = l.String("LOG_LEVEL", cfg.Log.Level)
	cfg.Log.Format = l.String("LOG_FORMAT", cfg.Log.Format)

	cfg.Client.CoreAddress = l.String("CORE_ADDRESS", cfg.Client.CoreAddress)
	cfg.Client.Token = l.String("TOKEN", cfg.Client.Token)
	cfg.Client.InstanceName = l.String("INSTANCE_NAME", cfg.Client.InstanceName)
	cfg.Client.MaxConcurrentRuns = int32(l.Int("MAX_CONCURRENT_RUNS", int(cfg.Client.MaxConcurrentRuns)))
	cfg.Client.FeatureJobInputNow = l.Bool("FEATURE_JOB_INPUT_NOW", cfg.Client.FeatureJobInputNow)
	cfg.Client.FeatureJobInputNext = l.Bool("FEATURE_JOB_INPUT_NEXT", cfg.Client.FeatureJobInputNext)

	cfg.Client.TLS.Enabled = l.Bool("TLS_ENABLED", cfg.Client.TLS.Enabled)
	cfg.Client.TLS.CAFile = l.String("TLS_CA_FILE", cfg.Client.TLS.CAFile)
	cfg.Client.TLS.ServerName = l.String("TLS_SERVER_NAME", cfg.Client.TLS.ServerName)
	cfg.Client.TLS.InsecureSkipVerify = l.Bool("TLS_INSECURE_SKIP_VERIFY", cfg.Client.TLS.InsecureSkipVerify)

	cfg.Client.DialTimeout = l.Duration("DIAL_TIMEOUT", cfg.Client.DialTimeout)
	cfg.Client.HeartbeatInterval = l.Duration("HEARTBEAT_INTERVAL", cfg.Client.HeartbeatInterval)
	cfg.Client.ReconnectMinBackoff = l.Duration("RECONNECT_MIN_BACKOFF", cfg.Client.ReconnectMinBackoff)
	cfg.Client.ReconnectMaxBackoff = l.Duration("RECONNECT_MAX_BACKOFF", cfg.Client.ReconnectMaxBackoff)

	cfg.Claude.Binary = l.String("CLAUDE_BINARY", cfg.Claude.Binary)
	cfg.Claude.DiscoveryRoots = l.StringSlice("DISCOVERY_ROOTS", cfg.Claude.DiscoveryRoots)

	cfg.StatePath = l.String("STATE_PATH", cfg.StatePath)

	// A CODE backend must support the whole mandatory CODE semantic contract;
	// the capability set is therefore not configurable here.
	cfg.Client.Capabilities = []backendv1.Capability{backendv1.Capability_CAPABILITY_CODE}

	if err := l.Err(); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks the loaded configuration.
func (c Config) Validate() error {
	if !logging.ValidLevel(c.Log.Level) {
		return errors.New(EnvPrefix + "LOG_LEVEL: unknown level " + c.Log.Level)
	}
	if !logging.ValidFormat(c.Log.Format) {
		return errors.New(EnvPrefix + "LOG_FORMAT: unknown format " + c.Log.Format)
	}
	if c.StatePath == "" {
		return errors.New(EnvPrefix + "STATE_PATH: a durable state path is required")
	}
	if c.Claude.Binary == "" {
		return errors.New(EnvPrefix + "CLAUDE_BINARY: an executable is required")
	}
	return c.Client.Validate()
}

// defaultInstanceName names the instance after the host it runs on.
func defaultInstanceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "claude-backend"
	}
	return host
}

// defaultStatePath is ~/.threavia/backend.db on a laptop; a Kubernetes backend
// points it at a small persistent volume.
func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "threavia", "backend.db")
	}
	return filepath.Join(home, ".threavia", "backend.db")
}
