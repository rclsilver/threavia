package postgres

import (
	"context"
	"slices"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func (s *Store) SessionHasPendingJobs(ctx context.Context, id domain.SessionID) (bool, error) {
	var pending bool
	err := s.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs j JOIN runs r ON r.id=j.run_id
	 WHERE r.session_id=$1 AND j.status NOT IN ('COMPLETED','FAILED','CANCELLED'))`, id).Scan(&pending)
	return pending, classify(err, "check pending session work")
}

type HandoffHistoryItem struct {
	Type  string   `json:"type"`
	Text  string   `json:"text,omitempty"`
	Files []string `json:"files,omitempty"`
}

// Select only useful fields, bounded in SQL, from before this Run's creation.
// Raw tool output, requests, provider ids and binary attachments never travel.
func (s *Store) HandoffHistory(ctx context.Context, owner domain.UserID, sessionID domain.SessionID, runID domain.RunID) ([]HandoffHistoryItem, error) {
	return s.sessionHistory(ctx, owner, sessionID, string(runID), false)
}

// ResumeHistory excludes the current prompt and later queued user messages.
func (s *Store) ResumeHistory(ctx context.Context, owner domain.UserID, sessionID domain.SessionID, jobID domain.JobID) ([]HandoffHistoryItem, error) {
	return s.sessionHistory(ctx, owner, sessionID, string(jobID), true)
}

func (s *Store) sessionHistory(ctx context.Context, owner domain.UserID, sessionID domain.SessionID, boundaryID string, resume bool) ([]HandoffHistoryItem, error) {
	boundary := "(SELECT min(global_sequence) FROM events WHERE run_id=$3 AND session_id=$1 AND type='run.created')"
	if resume {
		boundary = "(SELECT min(global_sequence) FROM events WHERE job_id=$3 AND session_id=$1 AND type='user.message')"
	}
	rows, err := s.q.Query(ctx, `SELECT e.type,
	 left(coalesce(e.payload->>'text', e.payload->>'summary', e.payload->>'error', ''),1500),
	 ARRAY(SELECT left(coalesce(f->>'path',''),200) FROM jsonb_array_elements(
	 CASE WHEN jsonb_typeof(e.payload->'files')='array' THEN e.payload->'files' ELSE '[]'::jsonb END) f LIMIT 20)
	 FROM events e JOIN sessions s ON s.id=e.session_id JOIN projects p ON p.id=s.project_id
	 WHERE s.id=$1 AND p.owner_id=$2 AND e.global_sequence < `+boundary+`
	 AND e.type IN ('user.message','agent.message','job.completed','job.failed','workspace.changed')
	 ORDER BY e.global_sequence DESC LIMIT 16`, sessionID, owner, boundaryID)
	if err != nil {
		return nil, classify(err, "read handoff history")
	}
	defer rows.Close()
	var result []HandoffHistoryItem
	for rows.Next() {
		var item HandoffHistoryItem
		if err := rows.Scan(&item.Type, &item.Text, &item.Files); err != nil {
			return nil, classify(err, "read handoff item")
		}
		result = append(result, item)
	}
	slices.Reverse(result)
	return result, classify(rows.Err(), "read handoff history")
}
