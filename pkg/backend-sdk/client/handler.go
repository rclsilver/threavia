package client

import (
	"context"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Handler receives the Core commands addressed to this backend.
//
// Every method is called from the control stream reader goroutine and must
// return quickly: long work belongs in a goroutine owned by the adapter. A
// returned error is reported to Core as a rejected CommandResult, it does not
// tear the connection down.
type Handler interface {
	// OnConnected is called once the Welcome lease has been received.
	OnConnected(ctx context.Context, welcome *backendv1.Welcome) error
	// OnDisconnected is called when the control stream drops, with the cause.
	OnDisconnected(ctx context.Context, cause error)

	// OnStartJob starts or resumes a Job. An empty native session id means a
	// fresh provider session must be created for the Run.
	OnStartJob(ctx context.Context, cmd *backendv1.StartJob) error
	// OnCancelJob asks the backend to stop a Job. The Job reaches CANCELLED only
	// once the backend confirms the stop with a job cancelled event.
	OnCancelJob(ctx context.Context, cmd *backendv1.CancelJob) error

	// OnValidationResolution delivers the user decision on a pending validation.
	OnValidationResolution(ctx context.Context, cmd *backendv1.ValidationResolution) error
	// OnUserInputResolution delivers the user answer to a pending input request.
	OnUserInputResolution(ctx context.Context, cmd *backendv1.UserInputResolution) error

	// OnJobInputNow interrupts and reorients the running work. Optional CODE
	// feature.
	OnJobInputNow(ctx context.Context, cmd *backendv1.JobInputNow) error
	// OnJobInputNext injects an instruction after the current action. Optional
	// CODE feature.
	OnJobInputNext(ctx context.Context, cmd *backendv1.JobInputNext) error

	// OnReconcileInstruction applies a Core reconciliation decision, typically
	// replaying events or reissuing a cancellation.
	OnReconcileInstruction(ctx context.Context, cmd *backendv1.ReconcileInstruction) error
}

// BaseHandler implements Handler by rejecting every command. Embed it to
// implement only the commands a backend actually supports.
type BaseHandler struct{}

// OnConnected implements Handler.
func (BaseHandler) OnConnected(context.Context, *backendv1.Welcome) error { return nil }

// OnDisconnected implements Handler.
func (BaseHandler) OnDisconnected(context.Context, error) {}

// OnStartJob implements Handler.
func (BaseHandler) OnStartJob(context.Context, *backendv1.StartJob) error {
	return ErrUnsupportedCommand
}

// OnCancelJob implements Handler.
func (BaseHandler) OnCancelJob(context.Context, *backendv1.CancelJob) error {
	return ErrUnsupportedCommand
}

// OnValidationResolution implements Handler.
func (BaseHandler) OnValidationResolution(context.Context, *backendv1.ValidationResolution) error {
	return ErrUnsupportedCommand
}

// OnUserInputResolution implements Handler.
func (BaseHandler) OnUserInputResolution(context.Context, *backendv1.UserInputResolution) error {
	return ErrUnsupportedCommand
}

// OnJobInputNow implements Handler.
func (BaseHandler) OnJobInputNow(context.Context, *backendv1.JobInputNow) error {
	return ErrUnsupportedCommand
}

// OnJobInputNext implements Handler.
func (BaseHandler) OnJobInputNext(context.Context, *backendv1.JobInputNext) error {
	return ErrUnsupportedCommand
}

// OnReconcileInstruction implements Handler.
func (BaseHandler) OnReconcileInstruction(context.Context, *backendv1.ReconcileInstruction) error {
	return ErrUnsupportedCommand
}
