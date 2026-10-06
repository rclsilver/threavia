package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
)

// ErrDuplicateEvent is returned when a backend event has already been
// persisted. Backend delivery is at-least-once and Core observation is
// deduplicated, so this is a normal outcome, never a failure.
var ErrDuplicateEvent = errors.New("event already recorded")

const eventColumns = `e.id, e.global_sequence, e.type, e.project_id, e.session_id,
	e.run_id, e.job_id, e.payload, e.occurred_at`

// AppendEvent persists an observable event and assigns it its global sequence.
//
// When the event carries a backend identity, a replay is detected by the
// (backend_instance_id, backend_event_id) uniqueness and reported as
// ErrDuplicateEvent, so that a duplicate never becomes a second user-visible
// event.
func (s *Store) AppendEvent(ctx context.Context, record *events.Record) error {
	if record.ID == "" {
		record.ID = domain.NewEventID()
	}
	payload := record.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	var (
		instanceID *domain.BackendInstanceID
		eventID    *string
		sequence   *uint64
	)
	if record.Origin != nil {
		instanceID = &record.Origin.BackendInstanceID
		eventID = &record.Origin.BackendEventID
		sequence = &record.Origin.BackendSequence
	}

	err := s.q.QueryRow(ctx, `
		INSERT INTO events (id, type, project_id, session_id, run_id, job_id,
		                    backend_instance_id, backend_event_id, backend_sequence,
		                    payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11, now()))
		ON CONFLICT (backend_instance_id, backend_event_id) DO NOTHING
		RETURNING global_sequence, occurred_at`,
		record.ID, record.Type, record.ProjectID, record.SessionID, record.RunID, record.JobID,
		instanceID, eventID, sequence, payload, nullableTime(record.Timestamp),
	).Scan(&record.Sequence, &record.Timestamp)

	if err := classify(err, "append event"); err != nil {
		// No row came back: the ON CONFLICT clause swallowed a replay.
		if errors.Is(err, ErrNotFound) {
			return ErrDuplicateEvent
		}
		return err
	}
	return nil
}

// SessionEvents returns a page of the Session timeline. With before set to zero
// it returns the most recent window; otherwise it pages backwards from that
// global sequence. The result is always in chronological order.
func (s *Store) SessionEvents(ctx context.Context, ownerID domain.UserID, sessionID domain.SessionID, before domain.Sequence, limit int) ([]events.Envelope, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+eventColumns+`
		FROM events e
		JOIN sessions sess ON sess.id = e.session_id
		JOIN projects p ON p.id = sess.project_id
		WHERE e.session_id = $1 AND p.owner_id = $2 AND ($3 = 0 OR e.global_sequence < $3)
		ORDER BY e.global_sequence DESC
		LIMIT $4`, sessionID, ownerID, before, limit)
	if err != nil {
		return nil, classify(err, "read session events")
	}
	defer rows.Close()

	var out []events.Envelope
	for rows.Next() {
		envelope, err := scanEnvelope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, envelope)
	}
	if err := classify(rows.Err(), "read session events"); err != nil {
		return nil, err
	}

	// The query walks backwards from the cursor; clients read forwards.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// EventsAfter returns the events a reconnecting client missed, across every
// Session it can see. This is the catch-up behind the global SSE cursor.
func (s *Store) EventsAfter(ctx context.Context, ownerID domain.UserID, after domain.Sequence, limit int) ([]events.Envelope, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+eventColumns+`
		FROM events e
		JOIN projects p ON p.id = e.project_id
		WHERE p.owner_id = $1 AND e.global_sequence > $2
		ORDER BY e.global_sequence
		LIMIT $3`, ownerID, after, limit)
	if err != nil {
		return nil, classify(err, "read events after cursor")
	}
	defer rows.Close()

	var out []events.Envelope
	for rows.Next() {
		envelope, err := scanEnvelope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, envelope)
	}
	return out, classify(rows.Err(), "read events after cursor")
}

// LatestSequence returns the current head of the global event sequence, used as
// the initial cursor handed to a client.
func (s *Store) LatestSequence(ctx context.Context) (domain.Sequence, error) {
	var sequence *domain.Sequence
	if err := s.q.QueryRow(ctx, `SELECT max(global_sequence) FROM events`).Scan(&sequence); err != nil {
		return 0, classify(err, "read latest sequence")
	}
	if sequence == nil {
		return 0, nil
	}
	return *sequence, nil
}

// LastBackendSequence returns the highest backend sequence Core persisted for a
// Job, so a reconnecting backend is told exactly where to replay from.
func (s *Store) LastBackendSequence(ctx context.Context, jobID domain.JobID) (uint64, error) {
	var sequence *uint64
	err := s.q.QueryRow(ctx,
		`SELECT max(backend_sequence) FROM events WHERE job_id = $1`, jobID).Scan(&sequence)
	if err != nil {
		return 0, classify(err, "read last backend sequence")
	}
	if sequence == nil {
		return 0, nil
	}
	return *sequence, nil
}

func scanEnvelope(row scanner) (events.Envelope, error) {
	var envelope events.Envelope
	err := row.Scan(&envelope.ID, &envelope.Sequence, &envelope.Type, &envelope.ProjectID,
		&envelope.SessionID, &envelope.RunID, &envelope.JobID, &envelope.Payload, &envelope.Timestamp)
	return envelope, classify(err, "read event")
}
