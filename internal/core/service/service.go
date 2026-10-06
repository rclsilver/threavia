// Package service holds the Core application services: the transactional
// operations clients and backends drive, sitting between the HTTP/gRPC edges and
// the storage layer.
//
// The operations this package will own, in the implementation order of
// THREAVIA_SPEC_V1.md section 31:
//
//   - StartSession: the atomic first-send creating Session + Run + Job + first
//     user message event, so no empty or partially-created Session can exist;
//   - EnqueueMessage: appending a Job to an existing Run, FIFO;
//   - CancelJob: RUNNING -> CANCELLING, reaching CANCELLED only once the backend
//     confirms the stop;
//   - ResolveValidation and ResolveUserInput: atomic pending -> resolved
//     transitions where the first valid response wins;
//   - IngestBackendEvent: deduplicated, globally sequenced event persistence;
//   - Reconcile: converging Core desired state with what a backend reports after
//     a reconnection.
//
// Commands that clients may realistically retry carry an idempotency key
// (spec section 27).
package service

import (
	"log/slog"

	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// Service is the Core application service root.
type Service struct {
	db       *postgres.DB
	backends *backendconn.Registry
	logger   *slog.Logger
}

// New builds the service root.
func New(db *postgres.DB, backends *backendconn.Registry, logger *slog.Logger) *Service {
	return &Service{db: db, backends: backends, logger: logger}
}

// DB exposes the database handle to the repositories built on top of it.
func (s *Service) DB() *postgres.DB { return s.db }

// Backends exposes the live backend control connections.
func (s *Service) Backends() *backendconn.Registry { return s.backends }
