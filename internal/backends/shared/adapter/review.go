package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
	"google.golang.org/protobuf/types/known/structpb"
)

type reviewGate struct {
	version         uint64
	required        bool
	correctionJobID string
}

func (a *Adapter) OnReviewWorkspaceRequest(ctx context.Context, req *backendv1.ReviewWorkspaceRequest) *backendv1.ReviewWorkspace {
	directory := req.GetDirectory()
	if directory == "" {
		directory = a.cfg.Provider.DefaultWorkingDirectory
	}
	var result map[string]any
	var err error
	switch req.GetOperation() {
	case "GATE":
		a.mu.Lock()
		if a.reviewGates == nil {
			a.reviewGates = make(map[string]reviewGate)
		}
		if gate := a.reviewGates[req.GetRunId()]; req.GetReviewVersion() >= gate.version {
			a.reviewGates[req.GetRunId()] = reviewGate{version: req.GetReviewVersion(), required: req.GetReviewRequired(), correctionJobID: req.GetCorrectionJobId()}
			for id, job := range a.jobs {
				if job.runID == req.GetRunId() {
					job.reviewRequired = req.GetReviewRequired() && id != req.GetCorrectionJobId()
				}
			}
		}
		a.mu.Unlock()
		result = map[string]any{"acknowledged": true}
	case "SNAPSHOT":
		result, err = workspace.CaptureReview(ctx, directory, req.GetRefId())
	case "LIST":
		var files []map[string]any
		files, err = workspace.ReviewFiles(ctx, directory, req.GetBaseTree(), req.GetHeadTree())
		result = map[string]any{"files": files}
	case "FILE":
		result, err = workspace.ReviewContent(ctx, directory, req.GetBaseTree(), req.GetHeadTree(), req.GetPath(), req.GetOldPath())
	default:
		err = fmt.Errorf("unknown review operation")
	}
	if err != nil {
		return &backendv1.ReviewWorkspace{Error: &backendv1.Error{Code: "UNAVAILABLE", Message: err.Error()}}
	}
	// Round-trip through JSON normalises slices to the Struct wire vocabulary.
	data, _ := json.Marshal(result)
	var value map[string]any
	_ = json.Unmarshal(data, &value)
	encoded, err := structpb.NewStruct(value)
	if err != nil {
		return &backendv1.ReviewWorkspace{Error: &backendv1.Error{Code: "ENCODING", Message: err.Error()}}
	}
	return &backendv1.ReviewWorkspace{Result: encoded}
}

// ReviewCheckpoint blocks at the next action while a required human review is
// pending. It is independent of provider permission allow rules and model text.
func (a *Adapter) ReviewCheckpoint(ctx context.Context, jobID string) error {
	a.mu.Lock()
	enabled := a.humanReview
	blocked := false
	if job := a.jobs[jobID]; job != nil {
		blocked = job.reviewRequired
	}
	a.mu.Unlock()
	if !enabled {
		return nil
	}
	sdk := a.sdk()
	if sdk == nil {
		return fmt.Errorf("review checkpoint has no backend connection")
	}
	// Unrestricted work keeps its established offline behavior. An acknowledged
	// mandatory review stays latched locally across a Core disconnection.
	if !sdk.Connected() {
		if blocked {
			return fmt.Errorf("human review is pending; reconnect Core and obtain approval before continuing")
		}
		return nil
	}
	// Bound the RPC below the native hook's timeout. A missing response must
	// produce an explicit deny, never a provider's fail-open hook timeout.
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	_, err := a.CallCoreTool(ctx, jobID, "review_checkpoint", map[string]any{}, "")
	if err != nil {
		a.mu.Lock()
		if job := a.jobs[jobID]; job != nil {
			blocked = job.reviewRequired
		}
		a.mu.Unlock()
		if !blocked && !sdk.Connected() {
			return nil
		}
	}
	return err
}
