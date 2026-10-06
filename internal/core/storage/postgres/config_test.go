package postgres

import (
	"strings"
	"testing"
	"time"
)

func TestDSNFromDiscreteFields(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Host:           "db.internal",
		Port:           5433,
		User:           "threavia",
		Password:       "s3cr3t",
		Database:       "threavia",
		SSLMode:        "require",
		MaxConns:       10,
		ConnectTimeout: 5 * time.Second,
	}

	dsn := cfg.DSN()
	for _, want := range []string{
		"postgres://", "threavia:s3cr3t@db.internal:5433/threavia",
		"sslmode=require", "connect_timeout=5",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q must contain %q", dsn, want)
		}
	}
}

// TestMigrationURLUsesPgxScheme pins the scheme expected by the golang-migrate
// pgx/v5 driver.
func TestMigrationURLUsesPgxScheme(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if got := cfg.MigrationURL(); !strings.HasPrefix(got, "pgx5://") {
		t.Errorf("MigrationURL() = %q, want a pgx5:// url", got)
	}

	cfg.URL = "postgres://user:pass@host:5432/db?sslmode=disable"
	if got := cfg.MigrationURL(); !strings.HasPrefix(got, "pgx5://") {
		t.Errorf("MigrationURL() with an explicit url = %q, want a pgx5:// url", got)
	}
}

// TestRedactedHidesThePassword pins that connection strings reaching the logs
// never carry credentials (specification section 28).
func TestRedactedHidesThePassword(t *testing.T) {
	t.Parallel()

	cases := []Config{
		{Host: "localhost", Port: 5432, User: "threavia", Password: "s3cr3t", Database: "threavia", MaxConns: 1},
		{URL: "postgres://threavia:s3cr3t@localhost:5432/threavia", MaxConns: 1},
	}

	for _, cfg := range cases {
		redacted := cfg.Redacted()
		if strings.Contains(redacted, "s3cr3t") {
			t.Errorf("Redacted() leaked the password: %q", redacted)
		}
		if !strings.Contains(redacted, "threavia") {
			t.Errorf("Redacted() = %q, want the user and database to remain visible", redacted)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"defaults", func(*Config) {}, false},
		{"explicit url", func(c *Config) { c.URL = "postgres://u@h:5432/d" }, false},
		{"missing host", func(c *Config) { c.Host = "" }, true},
		{"invalid port", func(c *Config) { c.Port = 0 }, true},
		{"missing user", func(c *Config) { c.User = "" }, true},
		{"missing database", func(c *Config) { c.Database = "" }, true},
		{"no connections", func(c *Config) { c.MaxConns = 0 }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
