package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the Core connection pool. Repositories are built on top of it.
type DB struct {
	pool *pgxpool.Pool
	cfg  Config
}

// Open creates the pool and verifies connectivity. Core fails fast when the
// database is unreachable: it is the single source of truth for platform state.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.ConnectTimeout > 0 {
		poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{pool: pool, cfg: cfg}, nil
}

// Pool exposes the underlying pgx pool.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Config returns the configuration the pool was built from.
func (db *DB) Config() Config { return db.cfg }

// Ping checks that the database is still reachable. It backs the Core readiness
// probe.
func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

// Close releases every pooled connection.
func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
	}
}
