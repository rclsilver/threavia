package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

// Service implements the backend control sink.
var _ backendconn.Sink = (*Service)(nil)

// dispatchedCommand remembers which Job a command belongs to, so a rejection
// fails that Job instead of being silently dropped.
//
// It is in-process state: a Core restart loses it, and the consequence is only
// that a late rejection is logged rather than acted on. Reconciliation then
// converges the Job anyway.
type dispatchedCommand struct {
	jobID domain.JobID
	at    time.Time
}

// Connected records a backend connection. Work waits for its initial report.
func (s *Service) Connected(ctx context.Context, instanceID domain.BackendInstanceID, connectionID string, hello *backendv1.Hello) error {
	capabilities := make([]domain.Capability, 0, len(hello.GetCapabilities()))
	for _, capability := range hello.GetCapabilities() {
		capabilities = append(capabilities, capabilityFromProto(capability))
	}

	var features []domain.Feature
	if hello.GetFeatures().GetHumanReview() {
		features = append(features, domain.FeatureHumanReview)
	}
	if hello.GetFeatures().GetModelSelection() {
		features = append(features, domain.FeatureModelSelection)
	}
	if hello.GetFeatures().GetPromptAttachments() {
		features = append(features, domain.FeaturePromptAttachments)
	}
	if hello.GetFeatures().GetJobInputNow() {
		features = append(features, domain.FeatureJobInputNow)
	}
	if hello.GetFeatures().GetJobInputNext() {
		features = append(features, domain.FeatureJobInputNext)
	}

	accepted, err := s.changeBackendState(ctx, instanceID, func(tx *postgres.Store, _ domain.BackendInstance) error {
		return tx.MarkBackendConnected(ctx, instanceID, connectionID,
			int(hello.GetProtocol().GetVersion()), capabilities,
			int(hello.GetCapacity().GetMaxConcurrentRuns()),
			hello.GetSdk().GetName(), hello.GetSdk().GetVersion(),
			hello.GetBackend().GetName(), hello.GetBackend().GetVersion(), features)
	})
	if err != nil {
		return err
	}
	if !accepted {
		return backendconn.ErrUnknownBackendToken
	}

	var catalog *domain.ModelCatalog
	if wire := hello.GetModelCatalog(); wire != nil {
		catalog = &domain.ModelCatalog{Models: []domain.ModelOption{}, DefaultModel: wire.GetDefaultModel(), DefaultEffort: wire.GetDefaultEffort()}
		for _, m := range wire.GetModels() {
			catalog.Models = append(catalog.Models, domain.ModelOption{ID: m.GetId(), Name: m.GetName(), Efforts: append([]string{}, m.GetEfforts()...), DefaultEffort: m.GetDefaultEffort()})
		}
	}
	b := &batch{}
	accepted, err = s.withBackendReport(ctx, instanceID, func(tx *postgres.Store, backend domain.BackendInstance) error {
		if err := tx.SetBackendModels(ctx, instanceID, catalog); err != nil {
			return err
		}
		if backend.OwnerID == nil {
			return nil
		}
		b.ownerID = *backend.OwnerID
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeBackendModelsUpdated, payload: map[string]any{"backendInstanceId": instanceID}})
	})
	if err != nil {
		return err
	}
	if !accepted {
		return backendconn.ErrUnknownBackendToken
	}
	s.flush(b)

	// The name follows the backend's configuration, the only place a person
	// can rename it. It is cosmetic, so a name another of the owner's backends
	// already holds keeps the old one rather than refusing the connection.
	if name := strings.TrimSpace(hello.GetInstanceName()); name != "" {
		if _, err := s.withBackendReport(ctx, instanceID, func(tx *postgres.Store, _ domain.BackendInstance) error {
			return tx.RenameBackendInstance(ctx, instanceID, name)
		}); err != nil {
			s.logger.Warn("cannot rename the backend instance",
				slog.String("backendInstanceId", string(instanceID)),
				slog.String("name", name), slog.String("error", err.Error()))
		}
	}

	// Even new work waits for reconciliation: a Job dispatched now would be
	// absent from the initial report and could be mistaken for lost work.
	return nil
}

// Disconnected parks the Jobs Core believed were live on a backend that is gone.
// They are not failed: the backend may well be finishing them locally, and it
// reports what actually happened when it comes back.
func (s *Service) Disconnected(ctx context.Context, instanceID domain.BackendInstanceID, connectionID string) {
	_, err := s.changeBackendState(ctx, instanceID, func(tx *postgres.Store, backend domain.BackendInstance) error {
		if backend.ConnectionID == nil || *backend.ConnectionID != connectionID {
			return nil
		}
		if err := tx.MarkBackendDisconnected(ctx, instanceID, connectionID); err != nil {
			return err
		}
		active, err := tx.ActiveJobsForBackend(ctx, instanceID)
		if err != nil {
			return err
		}
		for _, job := range active {
			// A cancellation in flight stays CANCELLING: it is reissued on
			// reconnection, as specification section 3.5 requires.
			if job.Status == domain.JobCancelling {
				continue
			}
			if _, err := tx.TransitionJob(ctx, job.ID, job.Status, domain.JobWaitingBackend, nil); err != nil &&
				!errors.Is(err, postgres.ErrNotFound) {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.logger.Error("cannot record the backend disconnection",
			slog.String("backendInstanceId", string(instanceID)), slog.String("error", err.Error()))
	}
}

// Heartbeat records backend liveness.
func (s *Service) Heartbeat(ctx context.Context, instanceID domain.BackendInstanceID, at time.Time, capacity *backendv1.Capacity) {
	if err := s.store.RecordBackendHeartbeat(ctx, instanceID, at, capacityFromProto(capacity)); err != nil {
		s.logger.Error("cannot record a heartbeat",
			slog.String("backendInstanceId", string(instanceID)), slog.String("error", err.Error()))
	}
}

// StatusUpdate records a self-reported operational status. OFFLINE is never
// accepted here: Core infers it from the connection.
func (s *Service) StatusUpdate(ctx context.Context, instanceID domain.BackendInstanceID, update *backendv1.StatusUpdate) {
	status := operationalStatusFromProto(update.GetStatus())
	if !status.SelfReportable() {
		s.logger.Warn("ignoring a non self-reportable backend status",
			slog.String("backendInstanceId", string(instanceID)), slog.String("status", status.String()))
		return
	}
	// Why it is in that state. Without this, the only place the answer existed
	// was a line in the Core log, where the person who has to fix it never
	// looks.
	accepted, err := s.changeBackendState(ctx, instanceID, func(tx *postgres.Store, _ domain.BackendInstance) error {
		if err := tx.UpdateBackendStatus(ctx, instanceID, status,
			providerAuthFromProto(update.GetProviderAuthState()), capacityFromProto(update.GetCapacity())); err != nil {
			return err
		}
		return tx.ReplaceBackendConditions(ctx, instanceID, conditionsFromProto(update.GetConditions()))
	})
	if err != nil {
		s.logger.Error("cannot record the backend status and conditions",
			slog.String("backendInstanceId", string(instanceID)), slog.String("error", err.Error()))
	}
	if accepted && err == nil && status == domain.BackendReady && providerAuthFromProto(update.GetProviderAuthState()) == domain.ProviderAuthenticated {
		go s.releaseQueuedWork(context.WithoutCancel(ctx), instanceID)
	}
}

// conditionsFromProto translates what a backend reported. Core stores the
// vocabulary the backend chose without interpreting it: a new provider must be
// able to explain itself without a Core release.
func conditionsFromProto(reported []*backendv1.Condition) []domain.Condition {
	out := make([]domain.Condition, 0, len(reported))
	for _, condition := range reported {
		if condition.GetType() == "" {
			continue
		}
		out = append(out, domain.Condition{
			Type:    condition.GetType(),
			Status:  condition.GetStatus(),
			Reason:  condition.GetReason(),
			Message: condition.GetMessage(),
		})
	}
	return out
}

// QuotaReport stores provider data; Core never queries a provider or sees its credentials.
func (s *Service) QuotaReport(ctx context.Context, instanceID domain.BackendInstanceID, report *backendv1.QuotaReport) {
	snapshot, err := quotas.FromStruct(report.GetSnapshot())
	if err != nil {
		s.logger.Warn("ignoring invalid quota snapshot", "backendInstanceId", instanceID, "error", err)
		return
	}
	b := &batch{}
	_, err = s.withBackendReport(ctx, instanceID, func(tx *postgres.Store, backend domain.BackendInstance) error {
		if err := tx.UpdateBackendQuotas(ctx, instanceID, snapshot); err != nil {
			return err
		}
		if backend.OwnerID == nil {
			return nil
		}
		b.ownerID = *backend.OwnerID
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeBackendQuotasUpdated, payload: map[string]any{"backendInstanceId": instanceID}})
	})
	if err != nil {
		s.logger.Error("cannot record backend quotas", "backendInstanceId", instanceID, "error", err)
		return
	}
	s.flush(b)
}

// ReconcileState converges Core desired state with what the backend reports
// actually happened locally.
//
// Core is the source of truth for what should happen; the backend is the source
// of truth for what did. So a Job Core thinks is cancelling gets its cancel
// reissued, a Job the backend does not know about is released back to the queue,
// and any event gap is replayed.
func (s *Service) ReconcileState(ctx context.Context, instanceID domain.BackendInstanceID, state *backendv1.ReconcileState) {
	known := make(map[domain.JobID]*backendv1.JobState)
	for _, run := range state.GetRuns() {
		if native := run.GetNativeSessionId(); native != "" && run.GetResumeStatus() != backendv1.ResumeStatus_RESUME_STATUS_UNAVAILABLE {
			s.bindReportedNativeSession(ctx, instanceID, domain.RunID(run.GetRunId()), native)
		}
		for _, job := range run.GetJobs() {
			known[domain.JobID(job.GetJobId())] = job
		}
	}

	active, err := s.store.ActiveJobsForBackend(ctx, instanceID)
	if err != nil {
		s.logger.Error("cannot read the active jobs for reconciliation",
			slog.String("backendInstanceId", string(instanceID)), slog.String("error", err.Error()))
		return
	}

	conn, connected := s.backends.Lookup(instanceID)
	var retry []domain.JobID
	for _, job := range active {
		reported, ok := known[job.ID]
		if !ok {
			// The backend has no memory of this Job: nothing is running, so it
			// goes back to the queue rather than waiting forever.
			s.logger.Info("backend does not know a job core believed active, requeueing",
				slog.String("jobId", string(job.ID)))
			if _, err := s.store.TransitionJob(ctx, job.ID, job.Status, domain.JobWaitingBackend, nil); err != nil &&
				!errors.Is(err, postgres.ErrNotFound) {
				s.logger.Error("cannot requeue a job", slog.String("jobId", string(job.ID)), slog.String("error", err.Error()))
			}
			retry = append(retry, job.ID)
			continue
		}

		if !connected {
			continue
		}

		// Ask for whatever Core has not persisted yet. A Job the backend already
		// finished converges through those replayed events, never by being run
		// again: the backend is the source of truth for what actually happened.
		persisted, err := s.store.LastBackendSequence(ctx, job.ID)
		if err != nil {
			s.logger.Error("cannot read the persisted sequence", slog.String("jobId", string(job.ID)), slog.String("error", err.Error()))
			continue
		}
		behind := reported.GetLastBackendSequence() > persisted
		if behind {
			conn.Send(&backendv1.CoreToBackend{
				CommandId: domain.NewUUID(),
				Message: &backendv1.CoreToBackend_ReconcileInstruction{
					ReconcileInstruction: &backendv1.ReconcileInstruction{
						RunId:               string(job.RunID),
						JobId:               string(job.ID),
						Action:              backendv1.ReconcileAction_RECONCILE_ACTION_REPLAY_EVENTS,
						FromBackendSequence: persisted + 1,
					},
				},
			})
		}

		// The backend says this Job is over and Core does not, with nothing left
		// to explain the difference: Core holds every event the backend
		// produced, the ending included, and simply never moved. The backend is
		// the source of truth for what actually happened locally (spec section
		// 9), so Core takes its word rather than waiting on a Job nothing is
		// running. Without this, a terminal event Core could not apply when it
		// arrived parks the Job for good, and every later message on that
		// Session queues behind it.
		//
		// While Core is behind, the ending is in the events it is about to
		// receive: converging through them keeps the timeline whole, so the
		// disagreement is left to settle itself.
		if !behind && terminalJobStatus(reported.GetStatus()) && !job.Status.Terminal() {
			s.settleFromBackend(ctx, job, reported.GetStatus())
			continue
		}

		// Core says CANCELLING and the backend is still working: reissue.
		if job.Status == domain.JobCancelling && !terminalJobStatus(reported.GetStatus()) {
			conn.Send(&backendv1.CoreToBackend{
				CommandId: domain.NewUUID(),
				Message: &backendv1.CoreToBackend_CancelJob{
					CancelJob: &backendv1.CancelJob{
						RunId: string(job.RunID), JobId: string(job.ID), Reason: "reissued after reconnection",
					},
				},
			})
		}

		// The backend is still on this Job, so whatever was already decided has
		// to reach it again.
		if !terminalJobStatus(reported.GetStatus()) {
			s.replayResolutions(ctx, conn, job)
		}
	}
	if connected {
		// Only release new work after the whole initial report is reconciled.
		// Jobs missing from that report are retried explicitly; jobs the backend
		// still holds remain parked until their events arrive.
		conn.MarkReconciled()
		for _, jobID := range retry {
			s.DispatchJob(ctx, jobID)
		}
		go s.releaseQueuedWork(context.WithoutCancel(ctx), instanceID)
	}
}

// bindReportedNativeSession records the native session a reconnecting backend
// reports for one of its Runs.
//
// The report is the backend's word, so it is held to what JobEvent holds an
// event to: a backend only speaks about the Runs it executes. Otherwise a
// backend owned by one person could set the native session of another
// person's Run, and the next Job there would resume it. The id also ends up on
// the provider's command line, so it passes the check a first send applies to
// an adopted session.
func (s *Service) bindReportedNativeSession(ctx context.Context, instanceID domain.BackendInstanceID, runID domain.RunID, native string) {
	logger := s.logger.With(slog.String("backendInstanceId", string(instanceID)), slog.String("runId", string(runID)))

	if _, err := adoptedSession(native); err != nil {
		logger.Warn("ignoring a malformed native session", slog.String("error", err.Error()))
		return
	}

	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		run, err := tx.LockRun(ctx, runID)
		if errors.Is(err, postgres.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if run.BackendInstanceID != instanceID {
			logger.Warn("ignoring a native session reported for a run of another backend")
			return nil
		}
		// Reconciliation is not a successful resume, even if its report
		// was received before the loss committed.
		if run.ResumeStatus == domain.ResumeUnavailable || run.ResumeReason != nil {
			return nil
		}
		return tx.BindNativeSession(ctx, runID, native)
	})
	if err != nil {
		logger.Error("cannot record a native session", slog.String("error", err.Error()))
	}
}

// replayResolutions re-sends the answers already given on a Job the backend is
// still running.
//
// An answer travels Core to backend exactly once, over the control stream and
// without an acknowledgement, so a stream that dies between the decision and
// its delivery takes the decision with it. Nothing brings it back on its own:
// the request is already RESOLVED, which is precisely what stops any client
// from answering it a second time. The tool call it was meant to unblock then
// waits for ever, its Job never ends, and every later message on that Session
// queues behind it — a Session wedged by a question that was, in fact,
// answered.
//
// Everything decided on the Job is re-sent rather than only what is still
// awaited, because only the backend knows which that is, and it is built for
// this: a resolution nothing waits for is dropped where it lands.
func (s *Service) replayResolutions(ctx context.Context, conn *backendconn.Connection, job domain.Job) {
	validations, err := s.store.ResolvedValidationsForJob(ctx, job.ID)
	if err != nil {
		s.logger.Error("cannot read the decisions to replay",
			slog.String("jobId", string(job.ID)), slog.String("error", err.Error()))
	}
	for _, validation := range validations {
		conn.Send(&backendv1.CoreToBackend{
			CommandId: domain.NewUUID(),
			Message: &backendv1.CoreToBackend_ValidationResolution{
				ValidationResolution: &backendv1.ValidationResolution{
					RunId:            string(job.RunID),
					JobId:            string(job.ID),
					RequestId:        validation.BackendRequestID,
					Approved:         validation.Approved != nil && *validation.Approved,
					ResolvedByUserId: derefUserID(validation.ResolvedByUserID),
					ResolvedAt:       resolvedAt(validation.ResolvedAt),
					Note:             derefString(validation.Note),
				},
			},
		})
	}

	inputs, err := s.store.ResolvedUserInputsForJob(ctx, job.ID)
	if err != nil {
		s.logger.Error("cannot read the answers to replay",
			slog.String("jobId", string(job.ID)), slog.String("error", err.Error()))
	}
	for _, input := range inputs {
		conn.Send(&backendv1.CoreToBackend{
			CommandId: domain.NewUUID(),
			Message: &backendv1.CoreToBackend_UserInputResolution{
				UserInputResolution: &backendv1.UserInputResolution{
					RunId:            string(job.RunID),
					JobId:            string(job.ID),
					RequestId:        input.BackendRequestID,
					Value:            derefString(input.Value),
					ResolvedByUserId: derefUserID(input.ResolvedByUserID),
					ResolvedAt:       resolvedAt(input.ResolvedAt),
				},
			},
		})
	}

	if len(validations)+len(inputs) > 0 {
		s.logger.Info("replayed the decisions of a job the backend is still running",
			slog.String("jobId", string(job.ID)),
			slog.Int("validations", len(validations)), slog.Int("userInputs", len(inputs)))
	}
}

// settleFromBackend ends a Job that Core still believes is live because the
// backend says it already ended.
//
// It records why, so the timeline does not simply show a Job that stopped for
// no stated reason: the event Core missed is gone, and saying "the backend
// reported this finished" is the honest replacement.
func (s *Service) settleFromBackend(ctx context.Context, job domain.Job, reported backendv1.JobStatus) {
	status := jobStatusFromProto(reported)
	s.logger.Info("settling a job the backend already ended",
		slog.String("jobId", string(job.ID)),
		slog.String("coreStatus", job.Status.String()),
		slog.String("backendStatus", status.String()))

	reason := "the backend reported this job as " + strings.ToLower(status.String()) +
		"; core had it as " + strings.ToLower(job.Status.String())

	var failure *string
	if status == domain.JobFailed {
		failure = &reason
	}
	if _, err := s.store.TransitionJob(ctx, job.ID, job.Status, status, failure); err != nil {
		s.logger.Error("cannot settle a job from the backend report",
			slog.String("jobId", string(job.ID)), slog.String("error", err.Error()))
		return
	}

	// The Session may have messages queued behind it, and they have waited long
	// enough.
	s.dispatchNext(ctx, job.RunID)
}

// jobStatusFromProto translates a terminal status a backend reported. Anything
// non-terminal becomes FAILED, because this is only ever called for a Job the
// backend says is over.
func jobStatusFromProto(status backendv1.JobStatus) domain.JobStatus {
	switch status {
	case backendv1.JobStatus_JOB_STATUS_COMPLETED:
		return domain.JobCompleted
	case backendv1.JobStatus_JOB_STATUS_CANCELLED:
		return domain.JobCancelled
	default:
		return domain.JobFailed
	}
}

// terminalJobStatus reports whether a backend says a Job is over.
func terminalJobStatus(status backendv1.JobStatus) bool {
	switch status {
	case backendv1.JobStatus_JOB_STATUS_COMPLETED,
		backendv1.JobStatus_JOB_STATUS_FAILED,
		backendv1.JobStatus_JOB_STATUS_CANCELLED:
		return true
	default:
		return false
	}
}

// JobEvent persists one backend event and applies its consequences, then
// returns the backend sequence Core has durably recorded for that Job.
func (s *Service) JobEvent(ctx context.Context, instanceID domain.BackendInstanceID, event *backendv1.JobEvent) (uint64, error) {
	jobID := domain.JobID(event.GetJobId())
	jc, err := s.store.LoadJobContext(ctx, jobID)
	if err != nil {
		return 0, fmt.Errorf("load job %s: %w", jobID, err)
	}
	// A backend may only speak about the Runs it actually executes.
	if jc.BackendInstanceID != instanceID {
		return 0, fmt.Errorf("backend %s does not own job %s", instanceID, jobID)
	}

	eventType, payload, err := translateJobEvent(event)
	if err != nil {
		return 0, err
	}

	if ended, ok := payload.(JobEndedPayload); ok {
		ended.Model = jc.Job.Model
		ended.Effort = jc.Job.Effort
		history, err := s.store.JobProviderHistory(ctx, jobID)
		if err != nil {
			return 0, err
		}
		ended.Usage = addProviderUsage(ended.Usage, readProviderHistory(history).Usage)
		payload = ended
	}
	if wait, ok := payload.(JobWaitingBackendPayload); ok {
		if wait.RetryAt == nil || !wait.RetryAt.After(s.now()) {
			next := s.now().Add(time.Minute)
			wait.RetryAt = &next
		}
		payload = wait
	}
	scope := domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, RunID: jc.Run.ID, JobID: jobID}
	b := &batch{ownerID: jc.OwnerID}
	duplicate := false
	isReviewCorrection := false
	switch event.GetBody().(type) {
	case *backendv1.JobEvent_JobCompleted, *backendv1.JobEvent_JobFailed, *backendv1.JobEvent_JobCancelled:
		review, err := s.store.ReadReview(ctx, jc.OwnerID, jc.SessionID)
		if err != nil {
			return 0, err
		}
		isReviewCorrection = review.CorrectionJobID == jc.Job.ID
	}

	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if eventType != "" {
			if isReviewCorrection {
				if _, err := tx.LockSession(ctx, jc.OwnerID, jc.SessionID); err != nil {
					return err
				}
			}
			record, err := s.newRecord(eventType, scope, payload)
			if err != nil {
				return err
			}
			record.Timestamp = occurredAt(event, s.now())
			record.Origin = &events.BackendOrigin{
				BackendInstanceID: instanceID,
				BackendEventID:    event.GetBackendEventId(),
				BackendSequence:   event.GetBackendSequence(),
			}
			switch err := tx.AppendEvent(ctx, record); {
			case errors.Is(err, postgres.ErrDuplicateEvent):
				// A replay: already part of history, and its consequences were
				// applied the first time.
				duplicate = true
				return nil
			case err != nil:
				return err
			}
			b.add(record)
		}
		if err := s.applyEvent(ctx, tx, b, jc, scope, event); err != nil {
			return err
		}
		switch event.GetBody().(type) {
		case *backendv1.JobEvent_JobCompleted, *backendv1.JobEvent_JobFailed, *backendv1.JobEvent_JobCancelled:
			return s.finishReviewCorrection(ctx, tx, b, jc)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	if !duplicate {
		s.flush(b)
		s.afterEvent(ctx, scope, event)
		s.wakeSessionManager(ctx, jc, event)
		s.notifyAttention(ctx, jc, event)
	} else {
		// Recover a callback if Core stopped after persisting the worker event
		// but before enqueueing its manager message. The callback key makes
		// an ordinary replay a no-op.
		s.wakeSessionManager(ctx, jc, event)
	}

	sequence, err := s.store.LastBackendSequence(ctx, jobID)
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

// applyEvent turns an event into the state changes it implies, inside the same
// transaction that persisted it.
func (s *Service) applyEvent(ctx context.Context, tx *postgres.Store, b *batch, jc postgres.JobContext, scope domain.Scope, event *backendv1.JobEvent) error {
	switch body := event.GetBody().(type) {
	case *backendv1.JobEvent_JobStarted:
		model, effort := body.JobStarted.GetModel(), body.JobStarted.GetEffort()
		if model == "" {
			model = jc.Job.Model
		}
		if effort == "" {
			effort = jc.Job.Effort
		}
		if err := tx.SetJobModel(ctx, scope.JobID, model, effort); err != nil {
			return err
		}
		return transitionTo(ctx, tx, scope.JobID, domain.JobRunning, nil)

	case *backendv1.JobEvent_NativeSessionBound:
		// The id ends up on the provider's command line for the next Job, so it
		// is held to what a reconnecting backend's report is held to. One that
		// fails is not bound, but the event is still kept and acknowledged:
		// refusing it would only have the backend replay it forever.
		native := body.NativeSessionBound.GetNativeSessionId()
		if _, err := adoptedSession(native); err != nil {
			s.logger.Warn("ignoring a malformed native session reported for a run",
				slog.String("backendInstanceId", string(jc.BackendInstanceID)),
				slog.String("runId", string(scope.RunID)),
				slog.String("error", err.Error()))
			return nil
		}
		run, err := tx.LockRun(ctx, scope.RunID)
		if err != nil {
			return err
		}
		job, err := tx.JobByID(ctx, scope.JobID)
		if err != nil || job.Status.Terminal() {
			return err
		}
		if run.ResumeStatus == domain.ResumeUnavailable && run.NativeSessionID != nil && *run.NativeSessionID == native {
			return nil
		}
		if err := tx.BindNativeSession(ctx, scope.RunID, native); err != nil {
			return err
		}
		if run.ResumeStatus == domain.ResumeUnavailable || run.ResumeReason != nil {
			reason := "RESUMED"
			if run.ResumeStatus == domain.ResumeUnavailable {
				reason = "NEW_SESSION"
			}
			return s.appendAll(ctx, tx, b, record{eventType: events.TypeRunResumeAvailable, scope: scope,
				payload: map[string]any{"reason": reason}})
		}
		return nil

	case *backendv1.JobEvent_ValidationRequested:
		payload, sum, err := canonicalJSON(body.ValidationRequested.GetRequestPayload())
		if err != nil {
			return err
		}
		request := domain.ValidationRequest{
			ID:               domain.NewValidationRequestID(),
			Scope:            scope,
			BackendRequestID: body.ValidationRequested.GetRequestId(),
			Status:           domain.AttentionPending,
			Title:            body.ValidationRequested.GetTitle(),
			Summary:          body.ValidationRequested.GetSummary(),
			RequestPayload:   payload,
			PayloadSHA256:    sum,
			Humanized:        body.ValidationRequested.GetHumanized(),
		}
		if err := tx.CreateValidationRequest(ctx, &request); err != nil {
			return err
		}
		// The event was staged before the request existed; stamp its id now so
		// clients can resolve it straight from the timeline.
		stampAttentionID(b, string(request.ID))
		return transitionTo(ctx, tx, scope.JobID, domain.JobWaitingValidation, nil)

	case *backendv1.JobEvent_UserInputRequested:
		request := domain.UserInputRequest{
			ID:               domain.NewUserInputRequestID(),
			Scope:            scope,
			BackendRequestID: body.UserInputRequested.GetRequestId(),
			Status:           domain.AttentionPending,
			Prompt:           body.UserInputRequested.GetPrompt(),
			Choices:          body.UserInputRequested.GetChoices(),
			FreeText:         body.UserInputRequested.GetFreeText(),
		}
		if err := tx.CreateUserInputRequest(ctx, &request); err != nil {
			return err
		}
		stampAttentionID(b, string(request.ID))
		return transitionTo(ctx, tx, scope.JobID, domain.JobWaitingInput, nil)

	case *backendv1.JobEvent_JobCompleted:
		if err := tx.AbandonJobAttention(ctx, scope.JobID, "the job ended"); err != nil {
			return err
		}
		return transitionTo(ctx, tx, scope.JobID, domain.JobCompleted, nil)

	case *backendv1.JobEvent_JobFailed:
		message := body.JobFailed.GetError().GetMessage()
		reason, permanent := resume.FailureReason(body.JobFailed.GetError().GetCode())
		attemptReason, attemptFailed := resume.AttemptFailureReason(body.JobFailed.GetError().GetCode())
		if permanent || attemptFailed {
			status, kind := domain.ResumeUnavailable, events.TypeRunResumeUnavailable
			if !permanent {
				status, kind, reason = domain.ResumeUnknown, events.TypeRunResumeFailed, attemptReason
			}
			run, err := tx.LockRun(ctx, scope.RunID)
			if err != nil {
				return err
			}
			job, err := tx.JobByID(ctx, scope.JobID)
			if err != nil {
				return err
			}
			if !job.Status.Terminal() && run.NativeSessionID != nil && run.ResumeStatus != domain.ResumeUnavailable {
				if err := tx.SetResumeStatus(ctx, scope.RunID, status, &reason); err != nil {
					return err
				}
				if err := s.appendAll(ctx, tx, b, record{eventType: kind, scope: scope,
					payload: map[string]any{"reason": reason}}); err != nil {
					return err
				}
			}
		}
		if err := tx.AbandonJobAttention(ctx, scope.JobID, "the job failed"); err != nil {
			return err
		}
		return transitionTo(ctx, tx, scope.JobID, domain.JobFailed, &message)

	case *backendv1.JobEvent_JobCancelled:
		if err := tx.AbandonJobAttention(ctx, scope.JobID, "the job was cancelled"); err != nil {
			return err
		}
		return transitionTo(ctx, tx, scope.JobID, domain.JobCancelled, nil)

	case *backendv1.JobEvent_JobWaitingBackend:
		job, err := tx.JobByID(ctx, scope.JobID)
		if err != nil {
			return err
		}
		if job.Status.Terminal() || job.Status == domain.JobCancelling {
			return nil
		}
		if err := tx.AbandonJobAttention(ctx, scope.JobID, "the provider attempt stopped"); err != nil {
			return err
		}
		if err := transitionTo(ctx, tx, scope.JobID, domain.JobWaitingBackend, nil); err != nil {
			return err
		}
		wait := &domain.BackendWait{Reason: body.JobWaitingBackend.GetReason(), Message: body.JobWaitingBackend.GetMessage()}
		if at := body.JobWaitingBackend.GetRetryAt(); at != nil && at.CheckValid() == nil {
			value := at.AsTime()
			wait.RetryAt = &value
		}
		if wait.RetryAt == nil || !wait.RetryAt.After(s.now()) {
			next := s.now().Add(time.Minute)
			wait.RetryAt = &next
		}
		return tx.SetJobBackendWait(ctx, scope.JobID, wait, body.JobWaitingBackend.GetActiveSeconds())

	default:
		return nil
	}
}

// afterEvent runs the consequences that must not be inside the transaction.
func (s *Service) afterEvent(ctx context.Context, scope domain.Scope, event *backendv1.JobEvent) {
	switch event.GetBody().(type) {
	case *backendv1.JobEvent_JobCompleted, *backendv1.JobEvent_JobFailed, *backendv1.JobEvent_JobCancelled:
		// The active slot of the Run is free: start the next queued Job.
		s.dispatchNext(ctx, scope.RunID)
		jc, err := s.store.LoadJobContext(ctx, scope.JobID)
		if err == nil {
			go func() {
				captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
				defer cancel()
				r, err := s.ReadReview(captureCtx, auth.Identity{UserID: jc.OwnerID}, jc.SessionID)
				if err == nil && len(r.Repositories) > 0 && (r.Status != "CORRECTING" || r.CorrectionJobID == jc.Job.ID) {
					_, err = s.refreshReview(captureCtx, auth.Identity{UserID: jc.OwnerID}, r)
				}
				if err != nil && !errors.Is(err, ErrConflict) {
					s.logger.Warn("could not capture completed review changes", "error", err)
				}
			}()
			s.wakeManagedAttention(ctx, jc.OwnerID, jc.SessionID)
		}
	}
}

// EphemeralJobEvent forwards a liveness-only signal. It is published to the
// connected clients and never persisted, so it never consumes a global
// sequence.
func (s *Service) EphemeralJobEvent(ctx context.Context, instanceID domain.BackendInstanceID, event *backendv1.EphemeralJobEvent) {
	jc, err := s.store.LoadJobContext(ctx, domain.JobID(event.GetJobId()))
	if err != nil || jc.BackendInstanceID != instanceID {
		return
	}
	projectID, sessionID, runID, jobID := jc.ProjectID, jc.SessionID, jc.Run.ID, jc.Job.ID
	s.broker.Publish(jc.OwnerID, events.Envelope{
		Timestamp: occurredAt2(event.GetOccurredAt().AsTime(), s.now()),
		Type:      events.Type(event.GetKind()),
		ProjectID: &projectID,
		SessionID: &sessionID,
		RunID:     &runID,
		JobID:     &jobID,
	})
}

// CommandResult fails the Job a backend refused to start, instead of leaving it
// RUNNING forever.
func (s *Service) CommandResult(ctx context.Context, instanceID domain.BackendInstanceID, result *backendv1.CommandResult) {
	value, ok := s.dispatched.LoadAndDelete(result.GetCommandId())
	if !ok || result.GetAccepted() {
		return
	}
	command := value.(dispatchedCommand)

	message := result.GetError().GetMessage()
	s.logger.Warn("backend rejected a command",
		slog.String("jobId", string(command.jobID)),
		slog.String("code", result.GetError().GetCode()),
		slog.String("message", message))

	jc, err := s.store.LoadJobContext(ctx, command.jobID)
	if err != nil {
		return
	}
	scope := domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, RunID: jc.Run.ID, JobID: command.jobID}
	if err := transitionTo(ctx, s.store, command.jobID, domain.JobFailed, &message); err != nil {
		s.logger.Error("cannot fail a rejected job", slog.String("jobId", string(command.jobID)), slog.String("error", err.Error()))
		return
	}
	s.emit(ctx, jc.OwnerID, events.TypeJobFailed, scope, JobEndedPayload{Error: message})
	s.dispatchNext(ctx, jc.Run.ID)
}

// releaseQueuedWork dispatches queued work after the backend's initial report.
func (s *Service) releaseQueuedWork(ctx context.Context, instanceID domain.BackendInstanceID) {
	jobs, err := s.store.QueuedJobsForBackend(ctx, instanceID)
	if err != nil {
		s.logger.Error("cannot read the queued jobs of a backend",
			slog.String("backendInstanceId", string(instanceID)), slog.String("error", err.Error()))
		return
	}
	for _, jobID := range jobs {
		s.DispatchJob(ctx, jobID)
	}
}

// transitionTo moves a Job to a status, ignoring a move that the state machine
// forbids or that another writer already made.
func transitionTo(ctx context.Context, tx *postgres.Store, jobID domain.JobID, to domain.JobStatus, failure *string) error {
	job, err := tx.JobByID(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status == to || !job.Status.CanTransition(to) {
		return nil
	}
	if _, err := tx.TransitionJob(ctx, jobID, job.Status, to, failure); err != nil {
		if errors.Is(err, postgres.ErrNotFound) || errors.Is(err, postgres.ErrJobSlotTaken) {
			return nil
		}
		return err
	}
	return nil
}

// stampAttentionID writes the Core identifier of a freshly created attention
// item into the event that announced it.
func stampAttentionID(b *batch, id string) {
	if len(b.records) == 0 {
		return
	}
	record := b.records[len(b.records)-1]
	var payload map[string]any
	if err := jsonUnmarshal(record.Payload, &payload); err != nil {
		return
	}
	switch record.Type {
	case events.TypeValidationRequested:
		payload["validationId"] = id
	case events.TypeUserInputRequested:
		payload["requestId"] = id
	default:
		return
	}
	if encoded, err := encodePayload(payload); err == nil {
		record.Payload = encoded
	}
}

func occurredAt(event *backendv1.JobEvent, fallback time.Time) time.Time {
	return occurredAt2(event.GetOccurredAt().AsTime(), fallback)
}

func occurredAt2(at, fallback time.Time) time.Time {
	if at.IsZero() || at.Unix() <= 0 {
		return fallback
	}
	return at
}

func capabilityFromProto(capability backendv1.Capability) domain.Capability {
	switch capability {
	case backendv1.Capability_CAPABILITY_CODE:
		return domain.CapabilityCode
	case backendv1.Capability_CAPABILITY_INTERACTION:
		return domain.CapabilityInteraction
	case backendv1.Capability_CAPABILITY_REVIEW:
		return domain.CapabilityReview
	default:
		return domain.Capability(capability.String())
	}
}

func operationalStatusFromProto(status backendv1.BackendOperationalStatus) domain.BackendOperationalStatus {
	switch status {
	case backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_STARTING:
		return domain.BackendStarting
	case backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY:
		return domain.BackendReady
	case backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED:
		return domain.BackendDegraded
	default:
		return domain.BackendOffline
	}
}

func providerAuthFromProto(state backendv1.ProviderAuthState) domain.ProviderAuthState {
	switch state {
	case backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED:
		return domain.ProviderAuthenticated
	case backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED:
		return domain.ProviderAuthenticationRequired
	default:
		return ""
	}
}

func capacityFromProto(capacity *backendv1.Capacity) domain.Capacity {
	return domain.Capacity{
		MaxConcurrentRuns: int(capacity.GetMaxConcurrentRuns()),
		ActiveRuns:        int(capacity.GetActiveRuns()),
	}
}
