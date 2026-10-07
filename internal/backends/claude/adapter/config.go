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
	// DiscoveryRoots are where the backend looks for a KnownDirectory that has
	// no binding here yet. They constrain discovery and search only: filesystem
	// access remains real OS behaviour (spec sections 11 and 28).
	DiscoveryRoots []string
	// DefaultWorkingDirectory is where a Job runs when its Session has no
	// working directory. Without it the agent would inherit whatever directory
	// the backend process happens to have been started from, which is accidental
	// rather than chosen.
	DefaultWorkingDirectory string
	// SkillCachePath is where Core-managed Skill bundles are unpacked and where
	// the per-Job plugin directories are assembled (spec section 18).
	SkillCachePath string
	// LocalSkillRoots are directories holding Skills that exist only on this
	// backend. Core learns their names and never their content.
	LocalSkillRoots []string
	// LocalInstructions are backend-private rules appended to the project ones.
	// They are machine or provider specific and never travel to Core.
	LocalInstructions string
}

// RegistrationConfig is how a backend obtains its credential the first time
// (spec section 8). Once registered it stores its identity locally and never
// needs these again.
type RegistrationConfig struct {
	// CoreAPI is the Core HTTP base URL, used only to register.
	CoreAPI string
	// Token is a one-shot token a user created, which owns the instance
	// immediately.
	Token string
	// SharedKey is the shared registration key, which creates an UNCLAIMED
	// instance plus a one-time claim code.
	SharedKey string
}

// Config is the complete Claude backend configuration.
type Config struct {
	Log          LogConfig
	Client       client.Config
	Claude       ClaudeConfig
	Registration RegistrationConfig

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
		Log:    LogConfig{Level: logging.LevelInfo, Format: logging.FormatText},
		Client: clientCfg,
		Claude: ClaudeConfig{
			Binary:                  "claude",
			DefaultWorkingDirectory: defaultWorkingDirectory(),
			SkillCachePath:          defaultSkillCachePath(),
		},
		Registration: RegistrationConfig{CoreAPI: "http://localhost:8080"},
		StatePath:    defaultStatePath(),
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
	cfg.Claude.DefaultWorkingDirectory = l.String("DEFAULT_WORKING_DIRECTORY", cfg.Claude.DefaultWorkingDirectory)
	cfg.Claude.SkillCachePath = l.String("SKILL_CACHE_PATH", cfg.Claude.SkillCachePath)
	cfg.Claude.LocalSkillRoots = l.StringSlice("LOCAL_SKILL_ROOTS", cfg.Claude.LocalSkillRoots)
	cfg.Claude.LocalInstructions = l.String("LOCAL_INSTRUCTIONS", cfg.Claude.LocalInstructions)

	cfg.Registration.CoreAPI = l.String("CORE_API", cfg.Registration.CoreAPI)
	cfg.Registration.Token = l.String("REGISTRATION_TOKEN", cfg.Registration.Token)
	cfg.Registration.SharedKey = l.String("SHARED_KEY", cfg.Registration.SharedKey)

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
	if c.Claude.SkillCachePath == "" {
		return errors.New(EnvPrefix + "SKILL_CACHE_PATH: a directory is required to cache skill bundles")
	}
	if c.Claude.DefaultWorkingDirectory == "" {
		return errors.New(EnvPrefix + "DEFAULT_WORKING_DIRECTORY: a directory is required for sessions without one")
	}
	// Registration material is useless without somewhere to present it.
	if (c.Registration.Token != "" || c.Registration.SharedKey != "") && c.Registration.CoreAPI == "" {
		return errors.New(EnvPrefix + "CORE_API: required to register with core")
	}
	// The credential is resolved at startup, from the local identity or by
	// registering, so it is not required here.
	probe := c.Client
	if probe.Token == "" {
		probe.Token = "resolved-at-startup"
	}
	return probe.Validate()
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

// defaultWorkingDirectory is where a Session with no working directory runs.
// The user home is a deliberate, predictable choice; inheriting the directory
// the backend was launched from is not.
func defaultWorkingDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	return home
}

// defaultSkillCachePath sits beside the durable state: both are backend-local
// data that survives a restart and belongs to this machine.
func defaultSkillCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "threavia", "skills")
	}
	return filepath.Join(home, ".threavia", "skills")
}
