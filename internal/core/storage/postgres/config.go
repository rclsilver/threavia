// Package postgres owns the Core PostgreSQL connection and schema migrations.
//
// PostgreSQL stores current state, metadata and persistent events
// (THREAVIA_SPEC_V1.md sections 2 and 23). Current state is never rebuilt by
// replaying events.
package postgres

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// Config describes how to reach the Core database.
type Config struct {
	// URL, when set, is used as-is and takes precedence over the discrete
	// fields below.
	URL string

	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string

	MaxConns       int32
	MinConns       int32
	ConnectTimeout time.Duration

	// AutoMigrate runs pending migrations at Core startup.
	AutoMigrate bool
}

// DefaultConfig returns the development-oriented defaults.
func DefaultConfig() Config {
	return Config{
		Host:           "localhost",
		Port:           5432,
		User:           "threavia",
		Database:       "threavia",
		SSLMode:        "disable",
		MaxConns:       10,
		MinConns:       0,
		ConnectTimeout: 10 * time.Second,
		AutoMigrate:    true,
	}
}

// Validate checks that the configuration can produce a usable DSN.
func (c Config) Validate() error {
	if c.URL != "" {
		if _, err := url.Parse(c.URL); err != nil {
			return fmt.Errorf("invalid database url: %w", err)
		}
		return nil
	}
	if c.Host == "" {
		return errors.New("database host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid database port %d", c.Port)
	}
	if c.User == "" {
		return errors.New("database user is required")
	}
	if c.Database == "" {
		return errors.New("database name is required")
	}
	if c.MaxConns <= 0 {
		return errors.New("database max connections must be greater than zero")
	}
	return nil
}

// DSN returns the libpq-style connection URL used by the pgx pool.
func (c Config) DSN() string { return c.dsn("postgres") }

// MigrationURL returns the same connection target with the scheme expected by
// the golang-migrate pgx/v5 driver.
func (c Config) MigrationURL() string {
	if c.URL == "" {
		return c.dsn("pgx5")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return c.URL
	}
	parsed.Scheme = "pgx5"
	return parsed.String()
}

func (c Config) dsn(scheme string) string {
	if c.URL != "" {
		return c.URL
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   "/" + c.Database,
	}
	if c.Password != "" {
		u.User = url.UserPassword(c.User, c.Password)
	} else {
		u.User = url.User(c.User)
	}
	query := url.Values{}
	if c.SSLMode != "" {
		query.Set("sslmode", c.SSLMode)
	}
	if c.ConnectTimeout > 0 {
		query.Set("connect_timeout", strconv.Itoa(int(c.ConnectTimeout.Seconds())))
	}
	u.RawQuery = query.Encode()
	return u.String()
}

// Redacted returns the DSN with the password replaced, safe to log
// (spec section 28: logs must never carry credentials).
func (c Config) Redacted() string {
	parsed, err := url.Parse(c.DSN())
	if err != nil {
		return "postgres://<unparsable>"
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
		}
	}
	return parsed.String()
}
