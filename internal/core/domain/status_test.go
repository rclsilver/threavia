package domain

import (
	"errors"
	"testing"
)

func TestProjectStatusTransitions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		from, to ProjectStatus
		want     bool
	}{
		{ProjectActive, ProjectArchived, true},
		{ProjectArchived, ProjectActive, true},
		{ProjectActive, ProjectActive, false},
		{ProjectArchived, ProjectArchived, false},
		{ProjectStatus("DELETED"), ProjectActive, false},
		{ProjectActive, ProjectStatus("DELETED"), false},
	}

	for _, tc := range cases {
		if got := tc.from.CanTransition(tc.to); got != tc.want {
			t.Errorf("ProjectStatus(%s).CanTransition(%s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestProjectStatusTransitionReturnsError(t *testing.T) {
	t.Parallel()

	got, err := ProjectActive.Transition(ProjectArchived)
	if err != nil {
		t.Fatalf("archiving an active project: unexpected error %v", err)
	}
	if got != ProjectArchived {
		t.Fatalf("archiving an active project = %s, want %s", got, ProjectArchived)
	}

	if _, err := ProjectArchived.Transition(ProjectArchived); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("re-archiving an archived project: got %v, want ErrInvalidTransition", err)
	}
}

func TestSessionStatusTransitions(t *testing.T) {
	t.Parallel()

	if !SessionActive.CanTransition(SessionArchived) {
		t.Error("a session must be archivable")
	}
	if !SessionArchived.CanTransition(SessionActive) {
		t.Error("an archived session must be restorable")
	}
	if SessionActive.CanTransition(SessionActive) {
		t.Error("a session must not transition to its own status")
	}
	if SessionStatus("UNKNOWN").Valid() {
		t.Error("an unknown session status must not be valid")
	}
}

// TestJobStatusTransitionMatrix pins the whole Job state machine of
// specification section 3.5.
func TestJobStatusTransitionMatrix(t *testing.T) {
	t.Parallel()

	all := []JobStatus{
		JobQueued, JobRunning, JobWaitingInput, JobWaitingValidation,
		JobWaitingBackend, JobCancelling, JobCompleted, JobFailed, JobCancelled,
	}

	allowed := map[JobStatus][]JobStatus{
		JobQueued:            {JobRunning, JobWaitingBackend, JobCancelled, JobFailed},
		JobRunning:           {JobWaitingInput, JobWaitingValidation, JobWaitingBackend, JobCancelling, JobCompleted, JobFailed},
		JobWaitingInput:      {JobRunning, JobWaitingBackend, JobCancelling, JobCompleted, JobFailed, JobCancelled},
		JobWaitingValidation: {JobRunning, JobWaitingBackend, JobCancelling, JobCompleted, JobFailed, JobCancelled},
		JobWaitingBackend:    {JobRunning, JobWaitingInput, JobWaitingValidation, JobCancelling, JobCompleted, JobFailed, JobCancelled},
		JobCancelling:        {JobCancelled, JobCompleted, JobFailed},
		JobCompleted:         nil,
		JobFailed:            nil,
		JobCancelled:         nil,
	}

	for _, from := range all {
		expected := make(map[JobStatus]bool, len(allowed[from]))
		for _, to := range allowed[from] {
			expected[to] = true
		}
		for _, to := range all {
			if got := from.CanTransition(to); got != expected[to] {
				t.Errorf("JobStatus(%s).CanTransition(%s) = %v, want %v", from, to, got, expected[to])
			}
		}
	}
}

// TestJobCancellationGoesThroughCancelling pins the cancel semantics: a RUNNING
// Job never jumps straight to CANCELLED, it must be confirmed by the backend.
func TestJobCancellationGoesThroughCancelling(t *testing.T) {
	t.Parallel()

	if JobRunning.CanTransition(JobCancelled) {
		t.Error("a running job must not reach CANCELLED without going through CANCELLING")
	}
	if !JobRunning.CanTransition(JobCancelling) {
		t.Error("a running job must be cancellable")
	}
	if !JobCancelling.CanTransition(JobCancelled) {
		t.Error("a cancelling job must reach CANCELLED once the backend confirms")
	}
	// A queued job has nothing running on a backend to confirm a stop.
	if !JobQueued.CanTransition(JobCancelled) {
		t.Error("a queued job must be cancellable directly")
	}
	if JobQueued.CanTransition(JobCancelling) {
		t.Error("a queued job has nothing to stop and must not reach CANCELLING")
	}
}

// TestJobCancellingMayStillSucceed pins the reconciliation rule: the backend is
// the source of truth for what actually happened locally.
func TestJobCancellingMayStillSucceed(t *testing.T) {
	t.Parallel()

	if !JobCancelling.CanTransition(JobCompleted) {
		t.Error("a job that completed before the cancellation arrived must be able to reach COMPLETED")
	}
	if !JobCancelling.CanTransition(JobFailed) {
		t.Error("a job that failed while cancelling must be able to reach FAILED")
	}
}

func TestJobStatusTerminalStatesAreFinal(t *testing.T) {
	t.Parallel()

	terminal := []JobStatus{JobCompleted, JobFailed, JobCancelled}
	every := []JobStatus{
		JobQueued, JobRunning, JobWaitingInput, JobWaitingValidation,
		JobWaitingBackend, JobCancelling, JobCompleted, JobFailed, JobCancelled,
	}

	for _, from := range terminal {
		if !from.Terminal() {
			t.Errorf("JobStatus(%s).Terminal() = false, want true", from)
		}
		for _, to := range every {
			if from.CanTransition(to) {
				t.Errorf("terminal JobStatus(%s) must not transition to %s", from, to)
			}
		}
	}
}

// TestJobActiveSlot pins that exactly the statuses occupying the single active
// slot of a Run report Active, matching the partial unique index in the schema.
func TestJobActiveSlot(t *testing.T) {
	t.Parallel()

	active := map[JobStatus]bool{
		JobRunning:           true,
		JobWaitingInput:      true,
		JobWaitingValidation: true,
		JobWaitingBackend:    true,
		JobCancelling:        true,
	}
	every := []JobStatus{
		JobQueued, JobRunning, JobWaitingInput, JobWaitingValidation,
		JobWaitingBackend, JobCancelling, JobCompleted, JobFailed, JobCancelled,
	}

	for _, status := range every {
		if got := status.Active(); got != active[status] {
			t.Errorf("JobStatus(%s).Active() = %v, want %v", status, got, active[status])
		}
	}
}

func TestJobWaitingStates(t *testing.T) {
	t.Parallel()

	waiting := map[JobStatus]bool{
		JobWaitingInput:      true,
		JobWaitingValidation: true,
		JobWaitingBackend:    true,
	}
	every := []JobStatus{
		JobQueued, JobRunning, JobWaitingInput, JobWaitingValidation,
		JobWaitingBackend, JobCancelling, JobCompleted, JobFailed, JobCancelled,
	}

	for _, status := range every {
		if got := status.Waiting(); got != waiting[status] {
			t.Errorf("JobStatus(%s).Waiting() = %v, want %v", status, got, waiting[status])
		}
	}
}

func TestJobStatusValid(t *testing.T) {
	t.Parallel()

	if !JobRunning.Valid() {
		t.Error("RUNNING must be a valid job status")
	}
	if JobStatus("PAUSED").Valid() {
		t.Error("there is no Pause in V1, PAUSED must not be a valid job status")
	}
}

func TestResumeStatusValid(t *testing.T) {
	t.Parallel()

	for _, status := range []ResumeStatus{ResumeUnknown, ResumeAvailable, ResumeUnavailable} {
		if !status.Valid() {
			t.Errorf("ResumeStatus(%s) must be valid", status)
		}
	}
	if ResumeStatus("EXPIRED").Valid() {
		t.Error("ResumeStatus(EXPIRED) must not be valid")
	}
}

// TestAttentionResolutionIsOneWay pins that a resolved validation or input
// request can never go back to pending: resolution is atomic and first wins.
func TestAttentionResolutionIsOneWay(t *testing.T) {
	t.Parallel()

	if !AttentionPending.CanTransition(AttentionResolved) {
		t.Error("a pending attention item must be resolvable")
	}
	if AttentionResolved.CanTransition(AttentionPending) {
		t.Error("a resolved attention item must never go back to pending")
	}
	if AttentionResolved.CanTransition(AttentionResolved) {
		t.Error("an attention item must not be resolved twice")
	}
	if _, err := AttentionResolved.Transition(AttentionPending); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reopening a resolved attention item: got %v, want ErrInvalidTransition", err)
	}
}

// TestAJobCanEndWhileWaiting is a regression test.
//
// The state machine let a waiting Job fail but not succeed, so an agent that
// gave up on an unanswered question and finished its turn produced a
// job.completed Core refused to apply. The Job stayed parked, and every later
// message on that Session queued behind it for good.
func TestAJobCanEndWhileWaiting(t *testing.T) {
	t.Parallel()

	for _, waiting := range []JobStatus{JobWaitingInput, JobWaitingValidation} {
		for _, ending := range []JobStatus{JobCompleted, JobFailed, JobCancelled} {
			if !waiting.CanTransition(ending) {
				t.Errorf("%s cannot end as %s, although the backend may report exactly that",
					waiting, ending)
			}
		}
	}
}
