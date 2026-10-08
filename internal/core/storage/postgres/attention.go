package postgres

import (
	"context"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const validationColumns = `v.id, v.project_id, v.session_id, v.run_id, v.job_id,
	v.backend_request_id, v.status, v.title, v.summary, v.request_payload,
	v.payload_sha256, v.humanized, v.approved, v.resolved_by_user_id,
	v.resolved_channel, v.note, v.created_at, v.resolved_at`

const userInputColumns = `u.id, u.project_id, u.session_id, u.run_id, u.job_id,
	u.backend_request_id, u.status, u.prompt, u.choices, u.free_text, u.value,
	u.resolved_by_user_id, u.resolved_channel, u.created_at, u.resolved_at`

// CreateValidationRequest inserts a pending permission request. A replayed
// backend event hits the (job_id, backend_request_id) uniqueness and returns the
// request already created, so a duplicate never produces a second prompt.
func (s *Store) CreateValidationRequest(ctx context.Context, v *domain.ValidationRequest) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO validation_requests (id, project_id, session_id, run_id, job_id,
		                                 backend_request_id, status, title, summary,
		                                 request_payload, payload_sha256, humanized)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, $8, $9, $10, $11)
		ON CONFLICT (job_id, backend_request_id) DO UPDATE SET id = validation_requests.id
		RETURNING id, created_at`,
		v.ID, v.Scope.ProjectID, v.Scope.SessionID, v.Scope.RunID, v.Scope.JobID,
		v.BackendRequestID, v.Title, v.Summary, v.RequestPayload, v.PayloadSHA256, v.Humanized,
	).Scan(&v.ID, &v.CreatedAt)
	return classify(err, "create validation request")
}

// ResolveValidationRequest performs the atomic pending to resolved transition.
// The first valid response wins; a later one finds nothing to update and gets
// ErrNotFound, which is how concurrent clients are arbitrated.
func (s *Store) ResolveValidationRequest(ctx context.Context, id domain.ValidationRequestID, approved bool, userID domain.UserID, channel, note string) (domain.ValidationRequest, error) {
	return scanValidationRequest(s.q.QueryRow(ctx, `
		UPDATE validation_requests v
		SET status = 'RESOLVED', approved = $2, resolved_by_user_id = $3,
		    resolved_channel = $4, note = NULLIF($5, ''), resolved_at = now()
		WHERE v.id = $1 AND v.status = 'PENDING'
		RETURNING `+validationColumns, id, approved, userID, channel, note))
}

// GetValidationRequest returns a request the user can access through its
// Project.
func (s *Store) GetValidationRequest(ctx context.Context, ownerID domain.UserID, id domain.ValidationRequestID) (domain.ValidationRequest, error) {
	return scanValidationRequest(s.q.QueryRow(ctx, `
		SELECT `+validationColumns+`
		FROM validation_requests v JOIN projects p ON p.id = v.project_id
		WHERE v.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// PendingValidations returns the permission requests currently waiting, for one
// Session or for every Session of the user when sessionID is empty.
func (s *Store) PendingValidations(ctx context.Context, ownerID domain.UserID, sessionID domain.SessionID) ([]domain.ValidationRequest, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+validationColumns+`
		FROM validation_requests v JOIN projects p ON p.id = v.project_id
		WHERE p.owner_id = $1 AND v.status = 'PENDING' AND ($2 = '' OR v.session_id = $2::uuid)
		ORDER BY v.created_at`, ownerID, string(sessionID))
	if err != nil {
		return nil, classify(err, "list pending validations")
	}
	defer rows.Close()

	var out []domain.ValidationRequest
	for rows.Next() {
		request, err := scanValidationRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, classify(rows.Err(), "list pending validations")
}

// CreateUserInputRequest inserts a pending request for an answer or a choice.
//
// The choices are coalesced because a free-text question legitimately has none,
// and an absent list must not be the difference between a question reaching the
// user and an event the backend replays forever.
func (s *Store) CreateUserInputRequest(ctx context.Context, u *domain.UserInputRequest) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO user_input_requests (id, project_id, session_id, run_id, job_id,
		                                 backend_request_id, status, prompt, choices, free_text)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, COALESCE($8::text[], '{}'), $9)
		ON CONFLICT (job_id, backend_request_id) DO UPDATE SET id = user_input_requests.id
		RETURNING id, created_at`,
		u.ID, u.Scope.ProjectID, u.Scope.SessionID, u.Scope.RunID, u.Scope.JobID,
		u.BackendRequestID, u.Prompt, u.Choices, u.FreeText,
	).Scan(&u.ID, &u.CreatedAt)
	return classify(err, "create user input request")
}

// ResolveUserInputRequest performs the atomic pending to resolved transition.
func (s *Store) ResolveUserInputRequest(ctx context.Context, id domain.UserInputRequestID, value string, userID domain.UserID, channel string) (domain.UserInputRequest, error) {
	return scanUserInputRequest(s.q.QueryRow(ctx, `
		UPDATE user_input_requests u
		SET status = 'RESOLVED', value = $2, resolved_by_user_id = $3,
		    resolved_channel = $4, resolved_at = now()
		WHERE u.id = $1 AND u.status = 'PENDING'
		RETURNING `+userInputColumns, id, value, userID, channel))
}

// GetUserInputRequest returns a request the user can access through its Project.
func (s *Store) GetUserInputRequest(ctx context.Context, ownerID domain.UserID, id domain.UserInputRequestID) (domain.UserInputRequest, error) {
	return scanUserInputRequest(s.q.QueryRow(ctx, `
		SELECT `+userInputColumns+`
		FROM user_input_requests u JOIN projects p ON p.id = u.project_id
		WHERE u.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// PendingUserInputs returns the input requests currently waiting.
func (s *Store) PendingUserInputs(ctx context.Context, ownerID domain.UserID, sessionID domain.SessionID) ([]domain.UserInputRequest, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+userInputColumns+`
		FROM user_input_requests u JOIN projects p ON p.id = u.project_id
		WHERE p.owner_id = $1 AND u.status = 'PENDING' AND ($2 = '' OR u.session_id = $2::uuid)
		ORDER BY u.created_at`, ownerID, string(sessionID))
	if err != nil {
		return nil, classify(err, "list pending user inputs")
	}
	defer rows.Close()

	var out []domain.UserInputRequest
	for rows.Next() {
		request, err := scanUserInputRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, classify(rows.Err(), "list pending user inputs")
}

// ResolvedValidationsForJob returns the decisions already taken on one Job,
// oldest first.
//
// Reconciliation reads them to re-send what a backend may never have received:
// a decision travels over the control stream once, and a stream that dies
// between the answer and its delivery takes the answer with it.
func (s *Store) ResolvedValidationsForJob(ctx context.Context, jobID domain.JobID) ([]domain.ValidationRequest, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+validationColumns+`
		FROM validation_requests v
		WHERE v.job_id = $1 AND v.status = 'RESOLVED'
		ORDER BY v.resolved_at`, jobID)
	if err != nil {
		return nil, classify(err, "list resolved validations")
	}
	defer rows.Close()

	var out []domain.ValidationRequest
	for rows.Next() {
		request, err := scanValidationRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, classify(rows.Err(), "list resolved validations")
}

// ResolvedUserInputsForJob returns the answers already given on one Job, oldest
// first, for the same reason as the validations above.
func (s *Store) ResolvedUserInputsForJob(ctx context.Context, jobID domain.JobID) ([]domain.UserInputRequest, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+userInputColumns+`
		FROM user_input_requests u
		WHERE u.job_id = $1 AND u.status = 'RESOLVED'
		ORDER BY u.resolved_at`, jobID)
	if err != nil {
		return nil, classify(err, "list resolved user inputs")
	}
	defer rows.Close()

	var out []domain.UserInputRequest
	for rows.Next() {
		request, err := scanUserInputRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, classify(rows.Err(), "list resolved user inputs")
}

// AbandonJobAttention resolves every pending item of a Job that ended without
// an answer, so a cancelled or failed Job leaves no ghost prompt behind.
func (s *Store) AbandonJobAttention(ctx context.Context, jobID domain.JobID, reason string) error {
	now := time.Now().UTC()
	if _, err := s.q.Exec(ctx, `
		UPDATE validation_requests
		SET status = 'RESOLVED', approved = FALSE, resolved_channel = 'system',
		    note = $2, resolved_at = $3
		WHERE job_id = $1 AND status = 'PENDING'`, jobID, reason, now); err != nil {
		return classify(err, "abandon pending validations")
	}
	_, err := s.q.Exec(ctx, `
		UPDATE user_input_requests
		SET status = 'RESOLVED', value = '', resolved_channel = 'system', resolved_at = $2
		WHERE job_id = $1 AND status = 'PENDING'`, jobID, now)
	return classify(err, "abandon pending user inputs")
}

func scanValidationRequest(row scanner) (domain.ValidationRequest, error) {
	var v domain.ValidationRequest
	err := row.Scan(&v.ID, &v.Scope.ProjectID, &v.Scope.SessionID, &v.Scope.RunID, &v.Scope.JobID,
		&v.BackendRequestID, &v.Status, &v.Title, &v.Summary, &v.RequestPayload,
		&v.PayloadSHA256, &v.Humanized, &v.Approved, &v.ResolvedByUserID,
		&v.ResolvedChannel, &v.Note, &v.CreatedAt, &v.ResolvedAt)
	return v, classify(err, "read validation request")
}

func scanUserInputRequest(row scanner) (domain.UserInputRequest, error) {
	var u domain.UserInputRequest
	err := row.Scan(&u.ID, &u.Scope.ProjectID, &u.Scope.SessionID, &u.Scope.RunID, &u.Scope.JobID,
		&u.BackendRequestID, &u.Status, &u.Prompt, &u.Choices, &u.FreeText, &u.Value,
		&u.ResolvedByUserID, &u.ResolvedChannel, &u.CreatedAt, &u.ResolvedAt)
	return u, classify(err, "read user input request")
}

// AttentionContexts says, for the Runs pending requests belong to, which
// session of which project they come from, which machine runs them and in
// which directory: what a person deciding a request needs to see.
func (s *Store) AttentionContexts(ctx context.Context, ownerID domain.UserID, runIDs []domain.RunID) (map[domain.RunID]domain.AttentionContext, error) {
	out := make(map[domain.RunID]domain.AttentionContext, len(runIDs))
	if len(runIDs) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(runIDs))
	for _, id := range runIDs {
		ids = append(ids, string(id))
	}
	rows, err := s.q.Query(ctx, `
		SELECT r.id, sess.title, p.name, b.name, COALESCE(kd.name, '')
		FROM runs r
		JOIN sessions sess ON sess.id = r.session_id
		JOIN projects p ON p.id = sess.project_id
		JOIN backend_instances b ON b.id = r.backend_instance_id
		LEFT JOIN known_directories kd ON kd.id = sess.working_directory_id
		WHERE r.id = ANY($1::uuid[]) AND p.owner_id = $2`, ids, ownerID)
	if err != nil {
		return nil, classify(err, "read attention context")
	}
	defer rows.Close()
	for rows.Next() {
		var (
			runID   domain.RunID
			context domain.AttentionContext
		)
		if err := rows.Scan(&runID, &context.SessionTitle, &context.ProjectName,
			&context.BackendName, &context.Directory); err != nil {
			return nil, classify(err, "read attention context")
		}
		out[runID] = context
	}
	return out, classify(rows.Err(), "read attention context")
}
