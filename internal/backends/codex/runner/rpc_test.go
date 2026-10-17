package runner

import (
	"bytes"
	"context"
	"encoding/json"
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
