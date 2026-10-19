package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// ReadReview uses the session's owner boundary, including before any review exists.
func (s *Store) ReadReview(ctx context.Context, owner domain.UserID, session domain.SessionID) (domain.Review, error) {
	if _, err := s.GetSession(ctx, owner, session); err != nil {
		return domain.Review{}, err
	}
	review := domain.Review{SessionID: session, Status: "OPEN", Repositories: []domain.ReviewRepository{}, Revisions: []domain.ReviewRevision{}, Threads: []domain.ReviewThread{}, Seen: map[string]string{}}
	var data []byte
	err := s.q.QueryRow(ctx, `SELECT document FROM human_reviews WHERE session_id=$1`, session).Scan(&data)
	if errors.Is(classify(err, "read review"), ErrNotFound) {
		return review, nil
	}
	if err != nil {
		return review, classify(err, "read review")
	}
	err = json.Unmarshal(data, &review)
	return review, err
}

// SaveReview is called under the session lock; each edit gets a monotonic version.
func (s *Store) SaveReview(ctx context.Context, review *domain.Review) error {
	review.Version++
	data, err := json.Marshal(review)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `INSERT INTO human_reviews(session_id,version,document) VALUES($1,$2,$3)
 ON CONFLICT(session_id) DO UPDATE SET version=EXCLUDED.version,document=EXCLUDED.document,updated_at=now()`, review.SessionID, review.Version, data)
	return classify(err, "save review")
}

// ReviewAllowsJob is checked while the caller holds the session lock.
func (s *Store) ReviewAllowsJob(ctx context.Context, session domain.SessionID, job domain.JobID) (bool, error) {
	var allowed bool
	err := s.q.QueryRow(ctx, `SELECT COALESCE((SELECT CASE WHEN document->>'status'='CORRECTING'
 THEN COALESCE(document->>'correctionJobId','')=$2
 ELSE NOT COALESCE((document->>'required')::boolean,false) OR document->>'status'='APPROVED' END
 FROM human_reviews WHERE session_id=$1),true)`, session, job).Scan(&allowed)
	return allowed, classify(err, "read review checkpoint")
}
