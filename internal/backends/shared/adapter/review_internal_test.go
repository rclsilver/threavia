package adapter

import (
	"context"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
	"io"
	"log/slog"
	"testing"
)

func TestReviewLatchSurvivesCoreOutageWithoutBlockingUnrestrictedWork(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := state.NewMemoryStore()
	a := New(Default(), &authProbeRunner{}, store, logger)
	a.humanReview = true
	cfg := Default().Client
	cfg.CoreAddress = "127.0.0.1:19091"
	cfg.Token = "fixture-token"
	cfg.TLS.Enabled = false
	sdk, err := client.New(cfg, a, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	a.Bind(sdk)
	a.jobs["work"] = &jobState{runID: "run"}
	a.jobs["correction"] = &jobState{runID: "run"}
	ctx := context.Background()
	if err = a.ReviewCheckpoint(ctx, "work"); err != nil {
		t.Fatalf("unrestricted work stopped during Core outage: %v", err)
	}
	gate := func(version uint64, required bool, correction string) {
		reply := a.OnReviewWorkspaceRequest(ctx, &backendv1.ReviewWorkspaceRequest{Operation: "GATE", RunId: "run", ReviewVersion: version, ReviewRequired: required, CorrectionJobId: correction})
		if reply.GetError() != nil {
			t.Fatal(reply.GetError())
		}
	}
	gate(2, true, "")
	if err = a.ReviewCheckpoint(ctx, "work"); err == nil {
		t.Fatal("Core outage bypassed acknowledged human review")
	}
	gate(1, false, "")
	if err = a.ReviewCheckpoint(ctx, "work"); err == nil {
		t.Fatal("out-of-order gate cleared newer review")
	}
	gate(3, true, "correction")
	if err = a.ReviewCheckpoint(ctx, "correction"); err != nil {
		t.Fatalf("authorized corrections blocked during outage: %v", err)
	}
	if err = a.ReviewCheckpoint(ctx, "work"); err == nil {
		t.Fatal("correction permission leaked into unrelated work")
	}
	gate(4, false, "")
	if err = a.ReviewCheckpoint(ctx, "work"); err != nil {
		t.Fatalf("approval did not release offline latch: %v", err)
	}
}
