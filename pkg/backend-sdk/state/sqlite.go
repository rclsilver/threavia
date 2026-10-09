package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite" // pure Go driver: a backend binary needs no C toolchain

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// schema is the local durable state of a BackendInstance.
//
// It is execution and recovery truth only (THREAVIA_SPEC_V1.md section 10):
// PostgreSQL remains the global platform truth, and nothing here is part of the
// wire protocol contract.
const schema = `
CREATE TABLE IF NOT EXISTS identity (
    singleton           INTEGER PRIMARY KEY CHECK (singleton = 1),
    backend_instance_id TEXT NOT NULL,
    token               TEXT NOT NULL,
    name                TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    run_id            TEXT PRIMARY KEY,
    native_session_id TEXT NOT NULL DEFAULT '',
    resume_status     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS jobs (
    job_id         TEXT PRIMARY KEY,
    run_id         TEXT NOT NULL,
    status         INTEGER NOT NULL DEFAULT 0,
    last_sequence  INTEGER NOT NULL DEFAULT 0,
    acked_sequence INTEGER NOT NULL DEFAULT 0
);

-- Events survive here until Core confirms persisting them, which is what lets a
-- backend keep working through a Core outage and replay afterwards.
CREATE TABLE IF NOT EXISTS pending_events (
    job_id   TEXT    NOT NULL,
    sequence INTEGER NOT NULL,
    payload  BLOB    NOT NULL,
    PRIMARY KEY (job_id, sequence)
);
`

// SQLiteStore is the durable Store implementation.
//
// SQLite is an implementation choice of the Go SDK, not a protocol contract: a
// backend in another language stores its state however it likes.
type SQLiteStore struct {
	db *sql.DB
	// SQLite serialises writers anyway; one mutex keeps the sequence allocation
	// a single atomic read-modify-write without a busy retry loop.
	mu sync.Mutex
}

// OpenSQLite opens, creating the file and its parent directory if needed. On a
// laptop this is ~/.threavia/backend.db; a Kubernetes backend points it at a
// small persistent volume. The logger hears about permissions that could not be
// tightened, which is worth knowing but not worth refusing to start over.
func OpenSQLite(path string, logger *slog.Logger) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("a state path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create the state directory: %w", err)
		}
		// MkdirAll leaves an existing directory as it was, and one made by an
		// earlier version or by a service manager is commonly 0755.
		tightenDirectory(dir, logger)
	}

	// The file holds the backend credential, which lets whoever reads it act as
	// this backend towards Core. Made before SQLite opens it, because SQLite
	// would create it with the process umask, and its -wal and -shm files take
	// the mode of the database they belong to.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create the local state: %w", err)
	}
	_ = file.Close()

	// WAL keeps a reader from blocking the writer, and the busy timeout absorbs
	// the brief contention between the event writer and the replay reader.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open the local state: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialise the local state: %w", err)
	}
	// After the first write, so the -wal and -shm files exist: a state left by
	// an earlier version is still 0644, and its companions with it.
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		tightenFile(file, logger)
	}
	return &SQLiteStore{db: db}, nil
}

// tightenFile makes a state file readable by this account only.
func tightenFile(path string, logger *slog.Logger) {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() == 0o600 {
		return
	}
	if err := os.Chmod(path, 0o600); err != nil {
		logger.Warn("cannot make the local state private to this account",
			slog.String("path", path), slog.String("error", err.Error()))
	}
}

// tightenDirectory closes the state directory to other accounts, if it is ours.
//
// Only if it is ours: a state path can name a file in a directory this account
// merely writes to, and closing someone else's directory is not this backend's
// call. As root every directory is ours, including shared ones such as
// /var/lib that a careless state path could name, so root leaves it alone.
func tightenDirectory(dir string, logger *slog.Logger) {
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0o077 == 0 || os.Getuid() == 0 || !ownedByUs(info) {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		logger.Warn("cannot make the state directory private to this account",
			slog.String("path", dir), slog.String("error", err.Error()))
	}
}

// LoadIdentity implements Store.
func (s *SQLiteStore) LoadIdentity(ctx context.Context) (Identity, bool, error) {
	var identity Identity
	err := s.db.QueryRowContext(ctx,
		`SELECT backend_instance_id, token, name FROM identity WHERE singleton = 1`).
		Scan(&identity.BackendInstanceID, &identity.Token, &identity.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, fmt.Errorf("read the local identity: %w", err)
	}
	return identity, true, nil
}

// SaveIdentity implements Store.
func (s *SQLiteStore) SaveIdentity(ctx context.Context, identity Identity) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO identity (singleton, backend_instance_id, token, name)
		VALUES (1, ?, ?, ?)
		ON CONFLICT (singleton) DO UPDATE SET
			backend_instance_id = excluded.backend_instance_id,
			token = excluded.token,
			name = excluded.name`,
		identity.BackendInstanceID, identity.Token, identity.Name)
	if err != nil {
		return fmt.Errorf("save the local identity: %w", err)
	}
	return nil
}

// SaveRun implements Store.
func (s *SQLiteStore) SaveRun(ctx context.Context, run RunRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (run_id, native_session_id, resume_status)
		VALUES (?, ?, ?)
		ON CONFLICT (run_id) DO UPDATE SET
			native_session_id = excluded.native_session_id,
			resume_status = excluded.resume_status`,
		run.RunID, run.NativeSessionID, int32(run.ResumeStatus))
	if err != nil {
		return fmt.Errorf("save a run: %w", err)
	}
	return nil
}

// Runs implements Store.
func (s *SQLiteStore) Runs(ctx context.Context) ([]RunRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, native_session_id, resume_status FROM runs ORDER BY run_id`)
	if err != nil {
		return nil, fmt.Errorf("read the local runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []RunRecord
	for rows.Next() {
		var (
			run    RunRecord
			status int32
		)
		if err := rows.Scan(&run.RunID, &run.NativeSessionID, &status); err != nil {
			return nil, fmt.Errorf("read a run: %w", err)
		}
		run.ResumeStatus = backendv1.ResumeStatus(status)
		out = append(out, run)
	}
	return out, rows.Err()
}

// SaveJob implements Store.
func (s *SQLiteStore) SaveJob(ctx context.Context, job JobRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (job_id, run_id, status, last_sequence, acked_sequence)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (job_id) DO UPDATE SET
			run_id = excluded.run_id,
			status = excluded.status,
			last_sequence = max(jobs.last_sequence, excluded.last_sequence),
			acked_sequence = max(jobs.acked_sequence, excluded.acked_sequence)`,
		job.JobID, job.RunID, int32(job.Status), job.LastSequence, job.AckedSequence)
	if err != nil {
		return fmt.Errorf("save a job: %w", err)
	}
	return nil
}

// Jobs implements Store.
func (s *SQLiteStore) Jobs(ctx context.Context) ([]JobRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT job_id, run_id, status, last_sequence, acked_sequence FROM jobs ORDER BY job_id`)
	if err != nil {
		return nil, fmt.Errorf("read the local jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []JobRecord
	for rows.Next() {
		var (
			job    JobRecord
			status int32
		)
		if err := rows.Scan(&job.JobID, &job.RunID, &status, &job.LastSequence, &job.AckedSequence); err != nil {
			return nil, fmt.Errorf("read a job: %w", err)
		}
		job.Status = backendv1.JobStatus(status)
		out = append(out, job)
	}
	return out, rows.Err()
}

// NextSequence implements Store. The sequence is per Job and survives a
// restart, which is what keeps Core deduplication and gap detection meaningful
// across a backend crash.
func (s *SQLiteStore) NextSequence(ctx context.Context, jobID string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sequence uint64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO jobs (job_id, run_id, last_sequence)
		VALUES (?, '', 1)
		ON CONFLICT (job_id) DO UPDATE SET last_sequence = jobs.last_sequence + 1
		RETURNING last_sequence`, jobID).Scan(&sequence)
	if err != nil {
		return 0, fmt.Errorf("allocate an event sequence: %w", err)
	}
	return sequence, nil
}

// AppendPending implements Store.
func (s *SQLiteStore) AppendPending(ctx context.Context, event *backendv1.JobEvent) error {
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode an event: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO pending_events (job_id, sequence, payload)
		VALUES (?, ?, ?)
		ON CONFLICT (job_id, sequence) DO UPDATE SET payload = excluded.payload`,
		event.GetJobId(), event.GetBackendSequence(), payload)
	if err != nil {
		return fmt.Errorf("buffer an event: %w", err)
	}
	return nil
}

// PendingEvents implements Store.
func (s *SQLiteStore) PendingEvents(ctx context.Context) ([]*backendv1.JobEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT payload FROM pending_events ORDER BY job_id, sequence`)
	if err != nil {
		return nil, fmt.Errorf("read the buffered events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*backendv1.JobEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("read a buffered event: %w", err)
		}
		event := &backendv1.JobEvent{}
		if err := proto.Unmarshal(payload, event); err != nil {
			return nil, fmt.Errorf("decode a buffered event: %w", err)
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

// Ack implements Store.
func (s *SQLiteStore) Ack(ctx context.Context, jobID string, sequence uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("acknowledge events: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pending_events WHERE job_id = ? AND sequence <= ?`, jobID, sequence); err != nil {
		return fmt.Errorf("drop acknowledged events: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE jobs SET acked_sequence = max(acked_sequence, ?) WHERE job_id = ?`, sequence, jobID); err != nil {
		return fmt.Errorf("record the acknowledged sequence: %w", err)
	}
	return tx.Commit()
}

// Close implements Store.
func (s *SQLiteStore) Close() error { return s.db.Close() }
