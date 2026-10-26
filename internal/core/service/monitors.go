package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/tools"
	"github.com/rclsilver/threavia/pkg/monitor"
	"google.golang.org/protobuf/types/known/structpb"
	"reflect"
	"time"
)

func (s *Service) ListMonitors(ctx context.Context, i auth.Identity, session, project string) ([]monitor.View, error) {
	if session != "" {
		if _, err := s.store.GetSession(ctx, i.UserID, domain.SessionID(session)); err != nil {
			return nil, translate(err)
		}
	}
	if project != "" {
		if _, err := s.store.GetProject(ctx, i.UserID, domain.ProjectID(project)); err != nil {
			return nil, translate(err)
		}
	}
	values, err := s.store.ListMonitors(ctx, i.UserID, session, project)
	for n := range values {
		_, online := s.backends.Lookup(domain.BackendInstanceID(values[n].BackendID))
		values[n].Confirmed = online && s.now().Sub(values[n].LastSeenAt) < 30*time.Second
	}
	return values, translate(err)
}
func (s *Service) monitorTool(ctx context.Context, i auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	if name == "monitor_list" {
		if len(input) != 0 {
			return nil, ErrInvalid
		}
		values, err := s.ListMonitors(ctx, i, string(jc.SessionID), "")
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"monitors": values})
	}
	if name == "monitor_stop" {
		if len(input) != 1 || !isUUID(text(input, "monitorId")) {
			return nil, ErrInvalid
		}
		v, err := s.store.Monitor(ctx, text(input, "monitorId"), false)
		if err != nil || v.OwnerID != i.UserID || v.SessionID != string(jc.SessionID) {
			return nil, ErrNotFound
		}
		if err = s.StopMonitor(ctx, i, v.ID); err != nil {
			return nil, err
		}
		return map[string]any{"monitorId": v.ID, "stopRequested": true}, nil
	}
	if name != "monitor_register" {
		return nil, ErrInvalid
	}
	for key := range input {
		if key != "command" && key != "description" && key != "timeoutSeconds" {
			return nil, ErrInvalid
		}
	}
	command, description := text(input, "command"), text(input, "description")
	seconds, err := integerInput(input, "timeoutSeconds", 3600, 1, 86400)
	if err != nil || command == "" || len(command) > 16384 || description == "" || len(description) > 240 {
		return nil, ErrInvalid
	}
	jcFull, err := s.store.LoadJobContext(ctx, jc.JobID)
	if err != nil {
		return nil, err
	}
	backend, err := s.store.BackendInstanceByID(ctx, jc.BackendInstanceID)
	if err != nil {
		return nil, err
	}
	if !backend.HasFeature(domain.FeaturePersistentMonitors) {
		return nil, fmt.Errorf("%w: this backend does not support persistent monitors", ErrConflict)
	}
	now := s.now()
	v := postgres.MonitorRecord{OwnerID: i.UserID, View: monitor.View{AutomaticResumesRemaining: 20, SessionID: string(jc.SessionID), BackendID: string(jc.BackendInstanceID), Snapshot: monitor.Snapshot{ID: domain.NewUUID(), RunID: string(jcFull.Run.ID), JobID: string(jc.JobID), Command: command, Description: description, State: "STARTING", StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Duration(seconds) * time.Second)}}}
	currentPolicy, err := s.store.EffectivePolicy(ctx, jc.JobID)
	if err != nil {
		return nil, err
	}
	if currentPolicy.MaxDurationSeconds > 0 && seconds > int64(currentPolicy.MaxDurationSeconds) {
		return nil, fmt.Errorf("%w: monitor duration exceeds policy", ErrInvalid)
	}
	v.Policy, err = json.Marshal(currentPolicy)
	if err != nil {
		return nil, err
	}
	b := &batch{ownerID: i.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if _, e := tx.LockSession(ctx, i.UserID, jc.SessionID); e != nil {
			return e
		}
		values, e := tx.ListMonitors(ctx, i.UserID, string(jc.SessionID), "")
		if e != nil {
			return e
		}
		active := 0
		for _, m := range values {
			if monitor.Active(m.State) {
				active++
			}
		}
		if active >= 5 {
			return fmt.Errorf("%w: this session already has five monitors", ErrConflict)
		}
		if e = tx.CreateMonitor(ctx, v); e != nil {
			return e
		}
		scope := domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, RunID: jcFull.Run.ID, JobID: jc.JobID}
		if e = s.recordLifecycleAudit(ctx, tx, i, events.TypeMonitorChanged, scope, v.ID, map[string]any{"action": "started", "description": description}); e != nil {
			return e
		}
		return s.appendAll(ctx, tx, b, record{events.TypeMonitorChanged, scope, v.View})
	})
	if err != nil {
		return nil, translate(err)
	}
	s.flush(b)
	return sessionToolResult(map[string]any{"monitor": v.View})
}

func (s *Service) StopMonitor(ctx context.Context, i auth.Identity, id string) error {
	b := &batch{ownerID: i.UserID}
	var v postgres.MonitorRecord
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		var err error
		v, err = tx.Monitor(ctx, id, true)
		if err != nil || v.OwnerID != i.UserID {
			return ErrNotFound
		}
		if !monitor.Active(v.State) || v.StopRequested {
			return nil
		}
		v.StopRequested = true
		if err = tx.SaveMonitor(ctx, v); err != nil {
			return err
		}
		session, err := tx.GetSession(ctx, i.UserID, domain.SessionID(v.SessionID))
		if err != nil {
			return err
		}
		scope := domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID}
		if err = s.recordLifecycleAudit(ctx, tx, i, events.TypeMonitorChanged, scope, v.ID, map[string]any{"action": "stop_requested", "description": v.Description}); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{events.TypeMonitorChanged, scope, map[string]any{"id": v.ID, "state": "STOPPING"}})
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	if conn, ok := s.backends.Lookup(domain.BackendInstanceID(v.BackendID)); ok {
		conn.Send(&backendv1.CoreToBackend{Message: &backendv1.CoreToBackend_MonitorControl{MonitorControl: &backendv1.MonitorControl{MonitorId: v.ID, RunId: v.RunID, Stop: true}}})
	}
	return nil
}

// MonitorReport authenticates the origin backend even after its Job ended.
// Snapshot sequence and wake idempotency protect against transport replays.
func (s *Service) MonitorReport(ctx context.Context, backend domain.BackendInstanceID, report *backendv1.MonitorReport) (*backendv1.MonitorControl, error) {
	if report.GetInventoryComplete() {
		return nil, s.reconcileMonitors(ctx, backend, report)
	}
	raw, err := report.GetSnapshot().MarshalJSON()
	if err != nil {
		return nil, err
	}
	var observed monitor.Snapshot
	if err = json.Unmarshal(raw, &observed); err != nil {
		return nil, err
	}
	if !isUUID(observed.ID) || !isUUID(observed.RunID) || len(observed.Output) > monitor.MaxOutput || len(observed.Error) > 4096 || observed.Sequence == 0 {
		return nil, ErrInvalid
	}
	switch observed.State {
	case "RUNNING", "STOPPING", "COMPLETED", "FAILED", "STOPPED", "EXPIRED", "INTERRUPTED":
	default:
		return nil, ErrInvalid
	}
	control := &backendv1.MonitorControl{MonitorId: observed.ID, RunId: observed.RunID}
	// A deleted origin has no state to retain, but its local process must stop.
	v, err := s.store.Monitor(ctx, observed.ID, false)
	if errors.Is(err, postgres.ErrNotFound) {
		control.Stop = true
		if !monitor.Active(observed.State) {
			control.AckSequence = observed.Sequence
		}
		return control, nil
	}
	if err != nil {
		return nil, err
	}
	if v.BackendID != string(backend) || v.RunID != observed.RunID || v.JobID != observed.JobID {
		return nil, ErrNotFound
	}
	b := &batch{ownerID: v.OwnerID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		current, err := tx.Monitor(ctx, observed.ID, true)
		if err != nil {
			return err
		}
		v = current
		session, err := tx.GetSession(ctx, v.OwnerID, domain.SessionID(v.SessionID))
		if err != nil {
			return err
		}
		run, err := tx.LatestRun(ctx, session.ID)
		if err != nil {
			return err
		}
		policy, err := tx.EffectivePolicy(ctx, domain.JobID(v.JobID))
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		var storedPolicy, currentPolicy any
		if err = json.Unmarshal(v.Policy, &storedPolicy); err != nil {
			return err
		}
		if err = json.Unmarshal(encoded, &currentPolicy); err != nil {
			return err
		}
		if session.Status != domain.SessionActive || string(run.ID) != v.RunID || !reflect.DeepEqual(storedPolicy, currentPolicy) {
			v.StopRequested = true
		}
		// Metadata is assigned by Core at registration, never replaced by reports.
		changed := observed.Sequence > v.Sequence && (monitor.Active(v.State) || v.Sequence == 0)
		if changed {
			v.State = observed.State
			v.Output = observed.Output
			v.Error = observed.Error
			v.Sequence = observed.Sequence
			v.Notify = observed.Notify
			v.UpdatedAt = observed.UpdatedAt
			if err = s.appendAll(ctx, tx, b, record{events.TypeMonitorChanged, domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID}, v.View}); err != nil {
				return err
			}
		}
		v.LastSeenAt = s.now()
		control.AckSequence = v.Sequence
		control.Stop = v.StopRequested
		return tx.SaveMonitor(ctx, v)
	})
	if err != nil {
		return nil, err
	}
	s.flush(b)
	// Deliver before acknowledging a terminal local snapshot; failures retry on
	// the next heartbeat. Queueing serializes with user work and human reviews.
	return s.wakeMonitor(ctx, v.ID, control)
}

func (s *Service) reconcileMonitors(ctx context.Context, backend domain.BackendInstanceID, report *backendv1.MonitorReport) error {
	if len(report.MonitorIds) > 1000 || report.GetConnectedAt() == nil || report.GetConnectedAt().CheckValid() != nil || report.GetConnectedAt().AsTime().After(s.now().Add(time.Second)) {
		return ErrInvalid
	}
	for _, id := range report.MonitorIds {
		if !isUUID(id) {
			return ErrInvalid
		}
	}
	ids := report.MonitorIds
	if ids == nil {
		ids = []string{}
	}
	missing, err := s.store.MissingMonitors(ctx, backend, report.ConnectedAt.AsTime(), ids)
	if err != nil {
		return err
	}
	for _, v := range missing {
		if monitor.Active(v.State) {
			v.State = "INTERRUPTED"
			v.Sequence++
			v.Notify = true
			v.Error = "The backend has no process for this monitor. The command was not restarted."
			v.UpdatedAt = s.now()
		}
		raw, _ := json.Marshal(v)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		st, err := structpb.NewStruct(p)
		if err != nil {
			return err
		}
		if _, err = s.MonitorReport(ctx, backend, &backendv1.MonitorReport{Snapshot: st}); err != nil {
			return err
		}
	}
	return nil
}
