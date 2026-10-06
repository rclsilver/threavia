package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors shared by every repository.
var (
	// ErrNotFound is returned when a row does not exist, or exists but is not
	// visible to the calling user: Core never distinguishes the two, so an
	// identifier cannot be probed for existence.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when a write violates a uniqueness or state
	// invariant, for instance a second active Job on the same Run.
	ErrConflict = errors.New("conflict")
)

// PostgreSQL error codes used to classify write failures.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// Querier is the subset of pgx shared by the pool and a transaction, so every
// repository method works inside or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is the Core repository root.
type Store struct {
	pool *pgxpool.Pool
	q    Querier
}

// NewStore builds the repository root over a connection pool.
func NewStore(db *DB) *Store {
	return &Store{pool: db.Pool(), q: db.Pool()}
}

// WithTx runs fn inside a transaction, with a Store bound to it. The
// transaction is committed when fn returns nil and rolled back otherwise.
//
// Every multi-row invariant of the specification goes through here: the atomic
// StartSession, and the pending to resolved transition where the first valid
// response wins.
func (s *Store) WithTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.pool == nil {
		return errors.New("nested transactions are not supported")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(&Store{q: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// classify maps a PostgreSQL error onto the package sentinels, so callers never
// have to know SQLSTATE codes.
func classify(err error, context string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation, pgForeignKeyViolation, pgCheckViolation:
			return fmt.Errorf("%s: %w: %s", context, ErrConflict, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("%s: %w", context, err)
}
