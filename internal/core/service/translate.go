package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/events"
)

// translateJobEvent maps a backend event onto the normalized Core timeline.
//
// This is where provider-shaped output becomes provider-independent history.
// An unknown body yields an empty type, and the event is acknowledged without
// being persisted: a backend speaking a newer protocol must not stall.
func translateJobEvent(event *backendv1.JobEvent) (events.Type, any, error) {
	switch body := event.GetBody().(type) {
	case *backendv1.JobEvent_JobStarted:
		return events.TypeJobStarted, nil, nil

	case *backendv1.JobEvent_NativeSessionBound:
		// The native session id is infrastructure, not timeline: it is recorded
		// on the Run, never shown as an event.
		return "", nil, nil

	case *backendv1.JobEvent_AgentMessage:
		return events.TypeAgentMessage, AgentMessagePayload{
			Text:        body.AgentMessage.GetText(),
			ArtifactIDs: body.AgentMessage.GetArtifactIds(),
		}, nil

	case *backendv1.JobEvent_ToolStarted:
		input, _, err := canonicalJSON(body.ToolStarted.GetInput())
		if err != nil {
			return "", nil, err
		}
		return events.TypeToolStarted, ToolPayload{
			ToolCallID: body.ToolStarted.GetToolCallId(),
			Name:       body.ToolStarted.GetName(),
			Input:      input,
		}, nil

	case *backendv1.JobEvent_ToolCompleted:
		output, _, err := canonicalJSON(body.ToolCompleted.GetOutput())
		if err != nil {
			return "", nil, err
		}
		return events.TypeToolCompleted, ToolPayload{
			ToolCallID: body.ToolCompleted.GetToolCallId(),
			Name:       body.ToolCompleted.GetName(),
			Output:     output,
		}, nil

	case *backendv1.JobEvent_ToolFailed:
		return events.TypeToolFailed, ToolPayload{
			ToolCallID: body.ToolFailed.GetToolCallId(),
			Name:       body.ToolFailed.GetName(),
			Error:      body.ToolFailed.GetError().GetMessage(),
		}, nil

	case *backendv1.JobEvent_ValidationRequested:
		payload, sum, err := canonicalJSON(body.ValidationRequested.GetRequestPayload())
		if err != nil {
			return "", nil, err
		}
		return events.TypeValidationRequested, ValidationRequestedPayload{
			Title:         body.ValidationRequested.GetTitle(),
			Summary:       body.ValidationRequested.GetSummary(),
			Payload:       payload,
			PayloadSHA256: sum,
			Humanized:     body.ValidationRequested.GetHumanized(),
		}, nil

	case *backendv1.JobEvent_UserInputRequested:
		return events.TypeUserInputRequested, UserInputRequestedPayload{
			Prompt:   body.UserInputRequested.GetPrompt(),
			Choices:  body.UserInputRequested.GetChoices(),
			FreeText: body.UserInputRequested.GetFreeText(),
		}, nil

	case *backendv1.JobEvent_WorkspaceChanged:
		files := make([]WorkspaceFile, 0, len(body.WorkspaceChanged.GetFiles()))
		for _, file := range body.WorkspaceChanged.GetFiles() {
			files = append(files, WorkspaceFile{Path: file.GetPath(), State: file.GetState().String()})
		}
		return events.TypeWorkspaceChanged, WorkspaceChangedPayload{
			KnownDirectoryID: body.WorkspaceChanged.GetKnownDirectoryId(),
			Files:            files,
			Additions:        body.WorkspaceChanged.GetAdditions(),
			Deletions:        body.WorkspaceChanged.GetDeletions(),
			Directory:        body.WorkspaceChanged.GetDirectory(),
			BaseTree:         body.WorkspaceChanged.GetBaseTree(),
			HeadTree:         body.WorkspaceChanged.GetHeadTree(),
		}, nil

	case *backendv1.JobEvent_JobCompleted:
		return events.TypeJobCompleted, JobEndedPayload{
			Summary: body.JobCompleted.GetSummary(),
			Usage:   usageFromProto(body.JobCompleted.GetUsage()),
		}, nil

	case *backendv1.JobEvent_JobFailed:
		return events.TypeJobFailed, JobEndedPayload{
			Error: body.JobFailed.GetError().GetMessage(),
			Usage: usageFromProto(body.JobFailed.GetUsage()),
		}, nil

	case *backendv1.JobEvent_JobCancelled:
		return events.TypeJobCancelled, JobEndedPayload{}, nil

	default:
		return "", nil, nil
	}
}

// canonicalJSON renders a protobuf Struct as deterministic JSON and returns its
// SHA-256.
//
// The hash is the security reference of a validation receipt (spec section 16),
// so the encoding must not depend on map iteration order: going through a Go map
// and encoding/json, which sorts object keys, gives a stable byte sequence for a
// given payload.
func canonicalJSON(value *structpb.Struct) (json.RawMessage, string, error) {
	if value == nil {
		encoded := json.RawMessage(`{}`)
		sum := sha256.Sum256(encoded)
		return encoded, hex.EncodeToString(sum[:]), nil
	}

	encoded, err := json.Marshal(value.AsMap())
	if err != nil {
		return nil, "", fmt.Errorf("canonicalise payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(sum[:]), nil
}
