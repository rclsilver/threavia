package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// Simulate the provider answering and closing stdout before the caller gets
// scheduled again. read must finish routing the response before closing done.
type replyThenExit struct{ rpc *rpc }

func (w replyThenExit) Write(b []byte) (int, error) {
	var request message
	if err := json.Unmarshal(b, &request); err != nil {
		return 0, err
	}
	reply, err := json.Marshal(map[string]any{"id": request.ID, "result": map[string]any{"value": "received"}})
	if err != nil {
		return 0, err
	}
	w.rpc.read(context.Background(), bytes.NewReader(reply))
	return len(b), nil
}

func TestRPCPreservesResponseBeforeEOF(t *testing.T) {
	for range 100 {
		r := &rpc{pending: make(map[string]chan message), done: make(chan struct{})}
		r.writer = replyThenExit{r}
		var result struct{ Value string }
		if err := r.call(context.Background(), "turn/start", map[string]any{}, &result); err != nil || result.Value != "received" {
			t.Fatalf("lost the last response on provider exit: %+v (%v)", result, err)
		}
	}
}

func TestRPCRoutesLargeMessages(t *testing.T) {
	// A resumed thread includes its history in a single response, which can
	// exceed the former 8 MiB scanner limit. Notifications share this reader.
	payload := json.RawMessage(`{"text":"` + strings.Repeat("x", (8<<20)+1) + `"}`)
	for _, response := range []bool{true, false} {
		name := "notification"
		large := message{Method: "item/completed", Params: payload}
		if response {
			name = "resume-response"
			large = message{ID: json.RawMessage(`"resume"`), Result: payload}
		}
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			encoder := json.NewEncoder(&output)
			for _, msg := range []message{large, {Method: "turn/completed"}} {
				if err := encoder.Encode(msg); err != nil {
					t.Fatal(err)
				}
			}
			reply := make(chan message, 1)
			r := &rpc{
				pending: map[string]chan message{`"resume"`: reply},
				events:  make(chan message, 2),
				done:    make(chan struct{}),
			}
			r.read(context.Background(), &output)
			if !errors.Is(r.err, io.ErrUnexpectedEOF) {
				t.Fatalf("reader stopped before provider EOF: %v", r.err)
			}
			queue := r.events
			if response {
				queue = reply
			}
			select {
			case got := <-queue:
				if !bytes.Equal(got.Result, large.Result) || !bytes.Equal(got.Params, large.Params) {
					t.Fatal("large message payload changed")
				}
			default:
				t.Fatal("large message was not routed")
			}
			select {
			case got := <-r.events:
				if got.Method != "turn/completed" {
					t.Fatalf("unexpected following message: %s", got.Method)
				}
			default:
				t.Fatal("message following large payload was lost")
			}
		})
	}
}

func TestRPCReadErrors(t *testing.T) {
	for _, input := range []string{"", `{"id":`, `{"id":!}`} {
		t.Run(input, func(t *testing.T) {
			r := &rpc{done: make(chan struct{})}
			r.read(context.Background(), strings.NewReader(input))
			if r.err == nil {
				t.Fatal("provider EOF or malformed JSON did not fail the reader")
			}
			if input == "" && !errors.Is(r.err, io.ErrUnexpectedEOF) {
				t.Fatalf("provider EOF lost its classification: %v", r.err)
			}
		})
	}
}
