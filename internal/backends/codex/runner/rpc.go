package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// app-server speaks newline-delimited JSON-RPC without a jsonrpc field.
// Responses are routed separately from notifications: an approval must never
// block reading the response to an interrupt or a new client request.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type rpc struct {
	writer   io.Writer
	writeMu  sync.Mutex
	mu       sync.Mutex
	sequence uint64
	pending  map[string]chan message
	events   chan message
	done     chan struct{}
	err      error
}

func newRPC(ctx context.Context, input io.Writer, output io.Reader) *rpc {
	r := &rpc{writer: input, pending: make(map[string]chan message), events: make(chan message, 256), done: make(chan struct{})}
	go r.read(ctx, output)
	return r
}
func (r *rpc) read(ctx context.Context, output io.Reader) {
	defer close(r.done)
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var msg message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			r.err = fmt.Errorf("invalid app-server message: %w", err)
			return
		}
		if msg.Method == "" && len(msg.ID) > 0 {
			r.mu.Lock()
			response := r.pending[string(msg.ID)]
			r.mu.Unlock()
			if response != nil {
				response <- msg
			}
			continue
		}
		select {
		case r.events <- msg:
		case <-ctx.Done():
			r.err = ctx.Err()
			return
		}
	}
	r.err = scanner.Err()
	if r.err == nil {
		r.err = io.ErrUnexpectedEOF
	}
}
func (r *rpc) send(value any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return json.NewEncoder(r.writer).Encode(value)
}
func (r *rpc) call(ctx context.Context, method string, params any, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	r.sequence++
	id := fmt.Sprintf("threavia-%d", r.sequence)
	keyBytes, _ := json.Marshal(id)
	key := string(keyBytes)
	response := make(chan message, 1)
	r.pending[key] = response
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, key); r.mu.Unlock() }()
	if err := r.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case msg := <-response:
		if msg.Error != nil {
			return fmt.Errorf("%s: %s (code %d)", method, msg.Error.Message, msg.Error.Code)
		}
		if result != nil {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.err
	}
}
func (r *rpc) respond(id json.RawMessage, result any, err error) error {
	if err != nil {
		return r.send(map[string]any{"id": id, "error": rpcError{Code: -32603, Message: err.Error()}})
	}
	return r.send(map[string]any{"id": id, "result": result})
}

var errFinishing = errors.New("the job is finishing and takes no more input")
