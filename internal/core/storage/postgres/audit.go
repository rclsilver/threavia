package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// AuditEntry is one recorded decision or privileged action (spec section 23).
//
// Its subject identifiers are plain text rather than foreign keys on purpose: an
// audit record has to survive the deletion of what it describes, which a
// cascading reference would not allow.
type AuditEntry struct {
	ID            string          `json:"id"`
	OwnerID       domain.UserID   `json:"ownerId"`
	ActorID       string          `json:"actorId"`
	Action        string          `json:"action"`
	ProjectID     string          `json:"projectId,omitempty"`
	SessionID     string          `json:"sessionId,omitempty"`
	JobID         string          `json:"jobId,omitempty"`
	SubjectID     string          `json:"subjectId,omitempty"`
	Channel       string          `json:"channel,omitempty"`
	PayloadSHA256 string          `json:"payloadSha256,omitempty"`
	Detail        json.RawMessage `json:"detail,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// RecordAudit appends an audit entry.
func (s *Store) RecordAudit(ctx context.Context, entry AuditEntry) error {
	detail := entry.Detail
	if len(detail) == 0 {
		detail = json.RawMessage(`{}`)
	}
	_, err := s.q.Exec(ctx, `
		INSERT INTO audit_entries (id, owner_id, actor_id, action, project_id,
		                           session_id, job_id, subject_id, channel,
		                           payload_sha256, detail)
		VALUES ($1, $2, $3, $4, NULLIF($5,''), NULLIF($6,''), NULLIF($7,''),
		        NULLIF($8,''), $9, $10, $11)`,
		domain.NewUUID(), entry.OwnerID, entry.ActorID, entry.Action,
		entry.ProjectID, entry.SessionID, entry.JobID, entry.SubjectID,
		entry.Channel, entry.PayloadSHA256, detail)
	return classify(err, "record an audit entry")
}

// ListAudit returns the audit trail of a user, most recent first.
func (s *Store) ListAudit(ctx context.Context, ownerID domain.UserID, limit int) ([]AuditEntry, error) {
	rows, err := s.q.Query(ctx, `
		SELECT id, owner_id, actor_id, action,
		       coalesce(project_id,''), coalesce(session_id,''), coalesce(job_id,''),
		       coalesce(subject_id,''), channel, payload_sha256, detail,
		       created_at
		FROM audit_entries
		WHERE owner_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, ownerID, limit)
	if err != nil {
		return nil, classify(err, "list audit entries")
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.ID, &entry.OwnerID, &entry.ActorID, &entry.Action,
			&entry.ProjectID, &entry.SessionID, &entry.JobID, &entry.SubjectID,
			&entry.Channel, &entry.PayloadSHA256, &entry.Detail, &entry.CreatedAt); err != nil {
			return nil, classify(err, "read an audit entry")
		}
		out = append(out, entry)
	}
	return out, classify(rows.Err(), "list audit entries")
}
