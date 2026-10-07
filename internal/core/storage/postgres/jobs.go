package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// ErrJobSlotTaken is returned when a Run already has an active Job. It
// surfaces the partial unique index enforcing the one-active-Job-per-Run
// invariant of specification section 3.4.
var ErrJobSlotTaken = errors.New("the run already has an active job")

const jobColumns = `j.id, j.run_id, j.status, j.idempotency_key, j.error, j.origin_channel,
	j.created_at, j.updated_at, j.started_at, j.ended_at`

// CreateJob inserts a Job. A Job is created QUEUED and is dispatched
// separately, so that enqueueing never depends on backend availability.
func (s *Store) CreateJob(ctx context.Context, job *domain.Job) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO jobs (id, run_id, status, idempotency_key, origin_channel)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		job.ID, job.RunID, job.Status, job.IdempotencyKey, job.OriginChannel,
	).Scan(&job.CreatedAt, &job.UpdatedAt)
	return classify(err, "create job")
}

// JobByIdempotencyKey returns the Job a retried request already created.
func (s *Store) JobByIdempotencyKey(ctx context.Context, key string) (domain.Job, error) {
	return scanJob(s.q.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs j WHERE j.idempotency_key = $1`, key))
}

// GetJob returns a Job the user can access through its Project.
func (s *Store) GetJob(ctx context.Context, ownerID domain.UserID, id domain.JobID) (domain.Job, error) {
	return scanJob(s.q.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM jobs j
		JOIN runs r ON r.id = j.run_id
		JOIN sessions s ON s.id = r.session_id
		JOIN projects p ON p.id = s.project_id
		WHERE j.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// JobByID returns a Job without an ownership check, for the backend-facing
// paths where the BackendInstance identity is already authenticated.
func (s *Store) JobByID(ctx context.Context, id domain.JobID) (domain.Job, error) {
	return scanJob(s.q.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs j WHERE j.id = $1`, id))
}

// ActiveJob returns the single active Job of a Run, if any.
func (s *Store) ActiveJob(ctx context.Context, runID domain.RunID) (domain.Job, error) {
	return scanJob(s.q.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM jobs j
		WHERE j.run_id = $1
		  AND j.status IN ('RUNNING','WAITING_INPUT','WAITING_VALIDATION','WAITING_BACKEND','CANCELLING')`, runID))
}

// NextQueuedJob returns the oldest queued Job of a Run. Queued Jobs are FIFO.
func (s *Store) NextQueuedJob(ctx context.Context, runID domain.RunID) (domain.Job, error) {
	return scanJob(s.q.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM jobs j
		WHERE j.run_id = $1 AND j.status = 'QUEUED'
		ORDER BY j.created_at
		LIMIT 1`, runID))
}

// ListJobs returns the Jobs of a Session, oldest first.
func (s *Store) ListJobs(ctx context.Context, sessionID domain.SessionID) ([]domain.Job, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+jobColumns+`
		FROM jobs j JOIN runs r ON r.id = j.run_id
		WHERE r.session_id = $1
		ORDER BY j.created_at`, sessionID)
	if err != nil {
		return nil, classify(err, "list jobs")
	}
	defer rows.Close()

	var out []domain.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, classify(rows.Err(), "list jobs")
}

// TransitionJob moves a Job from one status to another, atomically and only
// from the expected current status. It returns ErrNotFound when the Job already
// moved on, which is how a replayed backend event becomes a no-op, and
// ErrJobSlotTaken when another Job already occupies the active slot of the Run.
func (s *Store) TransitionJob(ctx context.Context, id domain.JobID, from, to domain.JobStatus, failure *string) (domain.Job, error) {
	var startedAt, endedAt *time.Time
	now := time.Now().UTC()
	if to == domain.JobRunning && !from.Active() {
		startedAt = &now
	}
	if to.Terminal() {
		endedAt = &now
	}

	job, err := scanJob(s.q.QueryRow(ctx, `
		UPDATE jobs j
		SET status = $3,
		    error = COALESCE($4, j.error),
		    started_at = COALESCE(j.started_at, $5),
		    ended_at = COALESCE($6, j.ended_at),
		    updated_at = now()
		WHERE j.id = $1 AND j.status = $2
		RETURNING `+jobColumns, id, from, to, failure, startedAt, endedAt))
	if err != nil && errors.Is(err, ErrConflict) {
		return job, ErrJobSlotTaken
	}
	return job, err
}

// DeleteQueuedJob removes a Job that has not started. Only queued Jobs can be
// deleted; a running one is cancelled instead.
func (s *Store) DeleteQueuedJob(ctx context.Context, ownerID domain.UserID, id domain.JobID) error {
	tag, err := s.q.Exec(ctx, `
		DELETE FROM jobs j
		USING runs r, sessions s, projects p
		WHERE j.run_id = r.id AND r.session_id = s.id AND s.project_id = p.id
		  AND j.id = $1 AND p.owner_id = $2 AND j.status = 'QUEUED'`, id, ownerID)
	if err != nil {
		return classify(err, "delete queued job")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobContext is everything needed to dispatch a Job to its backend, resolved in
// a single query.
type JobContext struct {
	Job         domain.Job
	Run         domain.Run
	SessionID   domain.SessionID
	ProjectID   domain.ProjectID
	ProjectName string
	ProjectDesc string
	// ProjectInstructions are the provider-independent project rules of
	// specification section 18. The backend maps them to its provider.
	ProjectInstructions string
	OwnerID             domain.UserID
	BackendInstanceID   domain.BackendInstanceID
	NativeSessionID     *string
	KnownDirectoryID    *domain.KnownDirectoryID
	// WorkingDirectoryPath is the physical path on this backend, empty when the
	// Session has no working directory or no binding exists for the backend.
	WorkingDirectoryPath *string
}

// LoadJobContext resolves a Job together with its Run, Session, Project and the
// working directory binding of the backend that will execute it.
func (s *Store) LoadJobContext(ctx context.Context, id domain.JobID) (JobContext, error) {
	var jc JobContext
	err := s.q.QueryRow(ctx, `
		SELECT j.id, j.run_id, j.status, j.idempotency_key, j.error, j.origin_channel,
		       j.created_at, j.updated_at, j.started_at, j.ended_at,
		       r.id, r.session_id, r.backend_instance_id, r.native_session_id,
		       r.resume_status, r.resume_reason, r.created_at, r.updated_at,
		       s.id, p.id, p.owner_id, p.name, p.description, p.instructions,
		       s.working_directory_id, b.path
		FROM jobs j
		JOIN runs r ON r.id = j.run_id
		JOIN sessions s ON s.id = r.session_id
		JOIN projects p ON p.id = s.project_id
		LEFT JOIN known_directory_bindings b
		       ON b.known_directory_id = s.working_directory_id
		      AND b.backend_instance_id = r.backend_instance_id
		WHERE j.id = $1`, id).Scan(
		&jc.Job.ID, &jc.Job.RunID, &jc.Job.Status, &jc.Job.IdempotencyKey, &jc.Job.Error,
		&jc.Job.OriginChannel,
		&jc.Job.CreatedAt, &jc.Job.UpdatedAt, &jc.Job.StartedAt, &jc.Job.EndedAt,
		&jc.Run.ID, &jc.Run.SessionID, &jc.Run.BackendInstanceID, &jc.Run.NativeSessionID,
		&jc.Run.ResumeStatus, &jc.Run.ResumeReason, &jc.Run.CreatedAt, &jc.Run.UpdatedAt,
		&jc.SessionID, &jc.ProjectID, &jc.OwnerID, &jc.ProjectName, &jc.ProjectDesc,
		&jc.ProjectInstructions,
		&jc.KnownDirectoryID, &jc.WorkingDirectoryPath,
	)
	if err != nil {
		return jc, classify(err, "load job context")
	}
	jc.BackendInstanceID = jc.Run.BackendInstanceID
	jc.NativeSessionID = jc.Run.NativeSessionID
	return jc, nil
}

// QueuedJobsForBackend returns the Jobs that have never been dispatched to one
// BackendInstance, oldest first.
//
// It deliberately excludes WAITING_BACKEND: such a Job was already sent
// somewhere and the backend may have finished it while Core was away, so
// re-dispatching it before reconciliation would run the work twice.
func (s *Store) QueuedJobsForBackend(ctx context.Context, instanceID domain.BackendInstanceID) ([]domain.JobID, error) {
	rows, err := s.q.Query(ctx, `
		SELECT j.id
		FROM jobs j JOIN runs r ON r.id = j.run_id
		WHERE r.backend_instance_id = $1 AND j.status = 'QUEUED'
		ORDER BY j.created_at`, instanceID)
	if err != nil {
		return nil, classify(err, "list queued jobs")
	}
	defer rows.Close()

	var out []domain.JobID
	for rows.Next() {
		var id domain.JobID
		if err := rows.Scan(&id); err != nil {
			return nil, classify(err, "list queued jobs")
		}
		out = append(out, id)
	}
	return out, classify(rows.Err(), "list queued jobs")
}

// ActiveJobsForBackend returns the Jobs Core believes are live on a backend,
// used to reconcile after a reconnection.
func (s *Store) ActiveJobsForBackend(ctx context.Context, instanceID domain.BackendInstanceID) ([]domain.Job, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+jobColumns+`
		FROM jobs j JOIN runs r ON r.id = j.run_id
		WHERE r.backend_instance_id = $1
		  AND j.status IN ('RUNNING','WAITING_INPUT','WAITING_VALIDATION','WAITING_BACKEND','CANCELLING')
		ORDER BY j.created_at`, instanceID)
	if err != nil {
		return nil, classify(err, "list active jobs")
	}
	defer rows.Close()

	var out []domain.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, classify(rows.Err(), "list active jobs")
}

func scanJob(row scanner) (domain.Job, error) {
	var job domain.Job
	err := row.Scan(&job.ID, &job.RunID, &job.Status, &job.IdempotencyKey, &job.Error, &job.OriginChannel,
		&job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.EndedAt)
	return job, classify(err, "read job")
}

// JobOriginChannels returns which kind of client started each of the given
// Jobs. It is read separately from the attention queries because it is a
// presentation concern of specification section 6, not part of what makes an
// item pending.
func (s *Store) JobOriginChannels(ctx context.Context, ids []domain.JobID) (map[domain.JobID]domain.Channel, error) {
	out := make(map[domain.JobID]domain.Channel, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := s.q.Query(ctx,
		`SELECT id, origin_channel FROM jobs WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, classify(err, "read job origin channels")
	}
	defer rows.Close()

	for rows.Next() {
		var id domain.JobID
		var origin domain.Channel
		if err := rows.Scan(&id, &origin); err != nil {
			return nil, classify(err, "read job origin channels")
		}
		out[id] = origin
	}
	return out, classify(rows.Err(), "read job origin channels")
}
