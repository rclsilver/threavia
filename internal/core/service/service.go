// Package service holds the Core application services: the transactional
// operations clients and backends drive, between the HTTP and gRPC edges and
// the storage layer.
//
// Two rules shape everything here, from THREAVIA_SPEC_V1.md sections 2 and 9:
// Core owns platform state while backends own execution, and Core is the source
// of truth for desired logical state while a backend is the source of truth for
// what actually happened locally.
package service

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/skills"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
)

// Errors the HTTP edge maps onto status codes.
var (
	// ErrNotFound covers both "does not exist" and "not yours": Core never lets
	// an identifier be probed for existence.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is a malformed or inconsistent request.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict is a request that contradicts current state.
	ErrConflict = errors.New("conflict")
	// ErrBackendUnavailable means the chosen BackendInstance has no live
	// control connection. Work is queued rather than lost.
	ErrBackendUnavailable = errors.New("backend is not connected")
)

// Service is the Core application service root.
type Service struct {
	store    *postgres.Store
	broker   *events.Broker
	backends *backendconn.Registry
	logger   *slog.Logger
	now      func() time.Time

	registration RegistrationConfig

	// objects backs Artifacts. It is nil when object storage is not configured,
	// and every Artifact operation then refuses rather than pretending.
	objects          s3.ObjectStore
	maxArtifactBytes int64

	// skills acquires Core-managed Project Skills. It is nil when Skill
	// acquisition is not configured.
	skills *skills.Acquirer

	// dispatched correlates an in-flight command with the Job it carries.
	dispatched sync.Map

	// diffs holds the diff requests waiting for a backend's answer, by
	// request id.
	diffs sync.Map
}

// Option customises a Service.
type Option func(*Service)

// WithClock overrides the clock, for tests.
func WithClock(fn func() time.Time) Option {
	return func(s *Service) { s.now = fn }
}

// New builds the service root.
func New(store *postgres.Store, broker *events.Broker, backends *backendconn.Registry, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		store:    store,
		broker:   broker,
		backends: backends,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Broker exposes the fan-out used by the SSE endpoint.
func (s *Service) Broker() *events.Broker { return s.broker }

// batch collects the events produced by one operation. They are published only
// once the transaction commits, so a client never observes an event describing
// state that was rolled back.
type batch struct {
	ownerID domain.UserID
	records []*events.Record
}

func (b *batch) add(record *events.Record) { b.records = append(b.records, record) }

// flush publishes a committed batch to the connected clients.
func (s *Service) flush(b *batch) {
	for _, record := range b.records {
		s.broker.Publish(b.ownerID, record.Envelope)
	}
}

// newRecord builds a Core-generated event. Core events carry no backend
// identity, which is what keeps them out of the backend deduplication index.
func (s *Service) newRecord(eventType events.Type, scope domain.Scope, payload any) (*events.Record, error) {
	encoded, err := encodePayload(payload)
	if err != nil {
		return nil, err
	}
	record := &events.Record{Envelope: events.Envelope{
		ID:        domain.NewEventID(),
		Timestamp: s.now(),
		Type:      eventType,
		Payload:   encoded,
	}}
	if scope.ProjectID != "" {
		record.ProjectID = &scope.ProjectID
	}
	if scope.SessionID != "" {
		record.SessionID = &scope.SessionID
	}
	if scope.RunID != "" {
		record.RunID = &scope.RunID
	}
	if scope.JobID != "" {
		record.JobID = &scope.JobID
	}
	return record, nil
}

// translate maps a storage error onto the service sentinels.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, postgres.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, postgres.ErrJobSlotTaken):
		return fmt.Errorf("%w: the run already has an active job", ErrConflict)
	case errors.Is(err, postgres.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, err)
	default:
		return err
	}
}
