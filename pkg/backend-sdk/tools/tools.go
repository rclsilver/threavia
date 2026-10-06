// Package tools is the backend side of the Core Tools integration.
//
// Core Tools are provider-independent (THREAVIA_SPEC_V1.md section 12): Core
// declares them in the ProjectContext sent at Job start, the backend adapter maps
// them to the provider tool mechanism, and every call is proxied to Core over the
// control stream. Tool names are never hardcoded in a backend.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// ErrNotConnected is returned when a tool call is attempted while no control
// stream is established.
var ErrNotConnected = errors.New("no active core connection")

// Call is one Core Tool invocation made by the agent.
type Call struct {
	RunID string
	JobID string
	Name  string
	Input *structpb.Struct
}

// Invoker executes a Core Tool call and waits for the Core response.
type Invoker interface {
	Invoke(ctx context.Context, call Call) (*structpb.Struct, error)
}

// Result is a Core Tool response.
type Result struct {
	Output *structpb.Struct
	Err    error
}

// Pending correlates in-flight tool requests with the responses arriving on the
// control stream.
type Pending struct {
	mu      sync.Mutex
	waiters map[string]chan Result
}

// NewPending returns an empty correlation table.
func NewPending() *Pending {
	return &Pending{waiters: make(map[string]chan Result)}
}

// Begin registers a request id and returns the channel its response arrives on.
func (p *Pending) Begin(requestID string) <-chan Result {
	ch := make(chan Result, 1)
	p.mu.Lock()
	p.waiters[requestID] = ch
	p.mu.Unlock()
	return ch
}

// Cancel drops a request id, for instance when the caller context expires.
func (p *Pending) Cancel(requestID string) {
	p.mu.Lock()
	delete(p.waiters, requestID)
	p.mu.Unlock()
}

// Complete delivers a Core response. Unknown request ids are ignored: a response
// may arrive after its caller gave up.
func (p *Pending) Complete(response *backendv1.CoreToolResponse) {
	p.mu.Lock()
	ch, ok := p.waiters[response.GetRequestId()]
	delete(p.waiters, response.GetRequestId())
	p.mu.Unlock()
	if !ok {
		return
	}

	result := Result{Output: response.GetResult()}
	if e := response.GetError(); e != nil {
		result.Err = fmt.Errorf("core tool error %s: %s", e.GetCode(), e.GetMessage())
	}
	ch <- result
}

// FailAll releases every waiter, used when the control stream drops.
func (p *Pending) FailAll(err error) {
	p.mu.Lock()
	waiters := p.waiters
	p.waiters = make(map[string]chan Result)
	p.mu.Unlock()

	for _, ch := range waiters {
		ch <- Result{Err: err}
	}
}

// Len returns the number of in-flight tool calls.
func (p *Pending) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.waiters)
}
