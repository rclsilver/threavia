package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolicyEndpointUsesJobTokenAndLifetime(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.sessions["private"] = &session{jobID: "job"}
	calls := 0
	s.RegisterPolicyGate("private", func(ctx context.Context, event map[string]any) (any, error) {
		calls++
		return map[string]any{"decision": "block", "reason": "policy refused"}, nil
	})
	call := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/policy/"+token, strings.NewReader(body))
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		s.handlePolicy(w, r)
		return w
	}
	if w := call("wrong", `{}`); w.Code != 404 || calls != 0 {
		t.Fatal("another token reached the gate")
	}
	if w := call("private", "broken"); w.Code != 400 || calls != 0 {
		t.Fatal("malformed event reached the gate")
	}
	if w := call("private", `{}`); w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), "policy refused") {
		t.Fatalf("missing decision: %v", w)
	}
	s.Unregister("private")
	if w := call("private", `{}`); w.Code != 404 || calls != 1 {
		t.Fatal("ended Job retained its gate")
	}
}

type gateAsker struct{ calls int }

func (*gateAsker) AskPermission(context.Context, string, string, map[string]any) (Decision, error) {
	panic("unexpected permission request")
}
func (*gateAsker) AskValidation(context.Context, string, string, map[string]any) (Decision, error) {
	panic("unexpected validation")
}
func (*gateAsker) AskUser(context.Context, string, string, []string, bool) (string, error) {
	panic("unexpected question")
}
func (a *gateAsker) CallCoreTool(context.Context, string, string, map[string]any, string) (map[string]any, error) {
	a.calls++
	return map[string]any{}, nil
}

func TestCoreToolPolicyGateRunsBeforeForwarding(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	asker := &gateAsker{}
	s.SetAsker(asker)
	sess := &session{jobID: "job", coreTools: []CoreTool{{Name: "directory_set", RequiresValidation: true}}}
	blocked := true
	sess.gate = func(ctx context.Context, event map[string]any) (any, error) {
		if event["tool_name"] != "mcp__threavia__directory_set" || event["requires_validation"] != true || event["threavia_direct"] != true || event["tool_input"] == nil {
			t.Fatalf("incorrect gate invocation: %+v", event)
		}
		if blocked {
			return map[string]any{"decision": "block"}, nil
		}
		return map[string]any{}, nil
	}
	raw := json.RawMessage(`{"name":"directory_set"}`)
	if result, err := s.call(context.Background(), sess, raw); err != nil || result.(map[string]any)["isError"] != true {
		t.Fatal("refused Core tool did not report an MCP error", result, err)
	}
	if asker.calls != 0 {
		t.Fatal("refused tool reached Core")
	}
	blocked = false
	if _, err := s.call(context.Background(), sess, raw); err != nil {
		t.Fatal(err)
	}
	if asker.calls != 1 {
		t.Fatal("approved tool did not reach Core")
	}
}
