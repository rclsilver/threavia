package postgres

import (
	"context"
	"encoding/json"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/monitor"
	"time"
)

type MonitorRecord struct {
	Policy json.RawMessage
	monitor.View
	OwnerID       domain.UserID
	StopRequested bool
	WakeSequence  uint64
	WakeCount     int
	WakeJobID     *domain.JobID
}

func (s *Store) CreateMonitor(ctx context.Context, v MonitorRecord) error {
	raw, err := json.Marshal(v.Snapshot)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `INSERT INTO monitors(id,owner_id,session_id,backend_id,snapshot,policy) VALUES($1,$2,$3,$4,$5,$6)`, v.ID, v.OwnerID, v.SessionID, v.BackendID, raw, v.Policy)
	return classify(err, "create monitor")
}
func (s *Store) Monitor(ctx context.Context, id string, lock bool) (MonitorRecord, error) {
	var v MonitorRecord
	var raw []byte
	sql := `SELECT owner_id,session_id,backend_id,snapshot,policy,last_seen_at,stop_requested,wake_sequence,wake_count,wake_job_id FROM monitors WHERE id=$1`
	if lock {
		sql += " FOR UPDATE"
	}
	err := s.q.QueryRow(ctx, sql, id).Scan(&v.OwnerID, &v.SessionID, &v.BackendID, &raw, &v.Policy, &v.LastSeenAt, &v.StopRequested, &v.WakeSequence, &v.WakeCount, &v.WakeJobID)
	if err != nil {
		return v, classify(err, "read monitor")
	}
	err = json.Unmarshal(raw, &v.Snapshot)
	v.AutomaticResumesRemaining = 20 - v.WakeCount
	return v, err
}
func (s *Store) SaveMonitor(ctx context.Context, v MonitorRecord) error {
	raw, err := json.Marshal(v.Snapshot)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `UPDATE monitors SET snapshot=$2,last_seen_at=$3,stop_requested=$4,wake_sequence=$5,wake_count=$6,wake_job_id=$7 WHERE id=$1`, v.ID, raw, v.LastSeenAt, v.StopRequested, v.WakeSequence, v.WakeCount, v.WakeJobID)
	return err
}
func (s *Store) ListMonitors(ctx context.Context, owner domain.UserID, session, project string) ([]monitor.View, error) {
	rows, err := s.q.Query(ctx, `SELECT m.session_id,m.backend_id,m.snapshot,m.last_seen_at,m.stop_requested,m.wake_count FROM monitors m JOIN sessions s ON s.id=m.session_id WHERE m.owner_id=$1 AND ($2='' OR m.session_id::text=$2) AND ($3='' OR s.project_id::text=$3) ORDER BY ((m.snapshot->>'state') IN ('STARTING','RUNNING','STOPPING')) DESC, m.last_seen_at DESC LIMIT 200`, owner, session, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []monitor.View{}
	for rows.Next() {
		var v monitor.View
		var raw []byte
		var stop bool
		var wakes int
		if err = rows.Scan(&v.SessionID, &v.BackendID, &raw, &v.LastSeenAt, &stop, &wakes); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v.Snapshot); err != nil {
			return nil, err
		}
		if stop && monitor.Active(v.State) {
			v.State = "STOPPING"
		}
		v.AutomaticResumesRemaining = 20 - wakes
		values = append(values, v)
	}
	return values, rows.Err()
}

// MissingMonitors excludes watches registered after this backend connection began.
func (s *Store) MissingMonitors(ctx context.Context, backend domain.BackendInstanceID, cutoff time.Time, ids []string) ([]monitor.Snapshot, error) {
	rows, err := s.q.Query(ctx, `SELECT snapshot FROM monitors WHERE backend_id=$1 AND (snapshot->>'startedAt')::timestamptz < $2 AND id <> ALL($3::uuid[]) AND ((snapshot->>'state') <> 'STARTING' OR (snapshot->>'startedAt')::timestamptz < now()-interval '30 seconds') AND ((snapshot->>'state') IN ('STARTING','RUNNING','STOPPING') OR ((snapshot->>'notify')::boolean AND wake_sequence < (snapshot->>'sequence')::bigint AND wake_count<20))`, backend, cutoff, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []monitor.Snapshot{}
	for rows.Next() {
		var raw []byte
		var v monitor.Snapshot
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
