package adapter_test

import (
	"context"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

func TestLostNativeSessionAndFailureSurviveAControlOutage(t *testing.T) {
	a, store := boundAdapter(t)
	ctx := context.Background()
	if err := store.SaveRun(ctx, state.RunRecord{RunID: "run", NativeSessionID: "old-native", ResumeStatus: backendv1.ResumeStatus_RESUME_STATUS_AVAILABLE}); err != nil {
		t.Fatal(err)
	}
	if err := a.JobFailed(ctx, "run", "job", resume.FailureCode("NOT_FOUND"), "The native conversation is unavailable", nil); err != nil {
		t.Fatal(err)
	}
	runs, err := store.Runs(ctx)
	if err != nil || len(runs) != 1 || runs[0].ResumeStatus != backendv1.ResumeStatus_RESUME_STATUS_UNAVAILABLE || runs[0].NativeSessionID != "old-native" {
		t.Fatal(runs, err)
	}
	pending, err := store.PendingEvents(ctx)
	if err != nil || len(pending) != 1 || pending[0].GetJobFailed().GetError().GetCode() != resume.FailureCode("NOT_FOUND") {
		t.Fatal(pending, err)
	}
	if err := a.NativeSessionBound(ctx, "run", "next-job", "fresh-native"); err != nil {
		t.Fatal(err)
	}
	runs, err = store.Runs(ctx)
	if err != nil || runs[0].ResumeStatus != backendv1.ResumeStatus_RESUME_STATUS_AVAILABLE || runs[0].NativeSessionID != "fresh-native" {
		t.Fatal(runs, err)
	}
}
