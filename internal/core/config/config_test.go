package config

import (
	"strings"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("loading the default configuration: %v", err)
	}

	if cfg.HTTP.Addr != ":8080" || cfg.GRPC.Addr != ":9090" {
		t.Errorf("listen addresses = %q and %q", cfg.HTTP.Addr, cfg.GRPC.Addr)
	}
	// SSE responses are long-lived: a write timeout would cut them.
	if cfg.HTTP.WriteTimeout != 0 {
		t.Errorf("HTTP write timeout = %s, want 0 so SSE streams are not cut", cfg.HTTP.WriteTimeout)
	}
	if cfg.Auth.Mode != auth.ModeNone {
		t.Errorf("default auth mode = %q, want %q", cfg.Auth.Mode, auth.ModeNone)
	}
	// Object storage is a V1 infrastructure requirement but the first slice runs
	// without it.
	if cfg.S3.Enabled {
		t.Error("object storage must be off by default")
	}
	// Shared-key registration is opt-in: by default a backend must present a
	// one-shot token a user created.
	if cfg.Backend.SharedRegistrationKey != "" {
		t.Error("shared key registration must be off by default")
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("THREAVIA_LOG_LEVEL", "debug")
	t.Setenv("THREAVIA_LOG_FORMAT", "json")
	t.Setenv("THREAVIA_HTTP_ADDR", "127.0.0.1:8081")
	t.Setenv("THREAVIA_GRPC_ADDR", "127.0.0.1:9091")
	t.Setenv("THREAVIA_POSTGRES_HOST", "db.internal")
	t.Setenv("THREAVIA_POSTGRES_PORT", "5433")
	t.Setenv("THREAVIA_POSTGRES_PASSWORD", "s3cr3t")
	t.Setenv("THREAVIA_POSTGRES_AUTO_MIGRATE", "false")
	t.Setenv("THREAVIA_S3_ENABLED", "true")
	t.Setenv("THREAVIA_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("THREAVIA_S3_BUCKET", "threavia")
	t.Setenv("THREAVIA_AUTH_MODE", "basic")
	t.Setenv("THREAVIA_AUTH_BASIC_USERNAME", "thomas")
	t.Setenv("THREAVIA_AUTH_BASIC_PASSWORD", "s3cr3t")
	t.Setenv("THREAVIA_BACKEND_HEARTBEAT_INTERVAL", "5s")
	t.Setenv("THREAVIA_BACKEND_OFFLINE_AFTER", "20s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	if cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Errorf("log configuration = %+v", cfg.Log)
	}
	if cfg.Postgres.Host != "db.internal" || cfg.Postgres.Port != 5433 {
		t.Errorf("postgres host/port = %s:%d", cfg.Postgres.Host, cfg.Postgres.Port)
	}
	if cfg.Postgres.AutoMigrate {
		t.Error("automatic migrations must be disabled")
	}
	if !cfg.S3.Enabled || cfg.S3.Bucket != "threavia" {
		t.Errorf("object storage configuration = %+v", cfg.S3)
	}
	if cfg.Auth.Mode != auth.ModeBasic {
		t.Errorf("auth mode = %q, want basic", cfg.Auth.Mode)
	}
	if cfg.Backend.HeartbeatInterval != 5*time.Second || cfg.Backend.OfflineAfter != 20*time.Second {
		t.Errorf("backend timings = %+v", cfg.Backend)
	}
}

func TestLoadReportsEveryInvalidValueAtOnce(t *testing.T) {
	t.Setenv("THREAVIA_POSTGRES_PORT", "not-a-port")
	t.Setenv("THREAVIA_BACKEND_HEARTBEAT_INTERVAL", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatal("an invalid configuration must be rejected")
	}
	for _, want := range []string{"THREAVIA_POSTGRES_PORT", "THREAVIA_BACKEND_HEARTBEAT_INTERVAL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %s", err, want)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"defaults", func(*Config) {}, false},
		{"unknown log level", func(c *Config) { c.Log.Level = "trace" }, true},
		{"unknown log format", func(c *Config) { c.Log.Format = "xml" }, true},
		{"no http address", func(c *Config) { c.HTTP.Addr = "" }, true},
		{"no grpc address", func(c *Config) { c.GRPC.Addr = "" }, true},
		{"tls certificate without key", func(c *Config) { c.GRPC.TLSCertFile = "cert.pem" }, true},
		{"tls pair", func(c *Config) { c.GRPC.TLSCertFile = "cert.pem"; c.GRPC.TLSKeyFile = "key.pem" }, false},
		{
			"offline threshold below heartbeat",
			func(c *Config) { c.Backend.OfflineAfter = c.Backend.HeartbeatInterval },
			true,
		},
		{"object storage without bucket", func(c *Config) { c.S3.Enabled = true; c.S3.Endpoint = "http://minio:9000" }, true},
		{"basic auth without password", func(c *Config) { c.Auth.Mode = auth.ModeBasic; c.Auth.BasicUsername = "u" }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)
			if err := cfg.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
