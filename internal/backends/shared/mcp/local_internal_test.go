package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestLocalToolsAreScopedAndErrorsReachProvider(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.sessions["codex"] = &session{jobID: "codex-job"}
	s.sessions["claude"] = &session{jobID: "claude-job"}
	refused := false
	calls := 0
	s.RegisterLocalTools("codex", []LocalTool{{Name: "web_search", Description: "Controlled search", InputSchema: map[string]any{"type": "object"}, Call: func(ctx context.Context, input map[string]any) (any, error) {
		calls++
		if input["query"] != "docs" {
			t.Fatal("tool arguments lost")
		}
		if refused {
			return nil, errors.New("policy refused this search")
		}
		return "search results", nil
	}}})
	for _, token := range []string{"codex", "claude"} {
		result, err := s.dispatch(context.Background(), s.sessions[token], request{Method: "tools/list"})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "web_search") != (token == "codex") {
			t.Fatal("local tool leaked to another Job")
		}
	}
	raw := json.RawMessage(`{"name":"web_search","arguments":{"query":"docs"}}`)
	if _, err := s.call(context.Background(), s.sessions["claude"], raw); err == nil || calls != 0 {
		t.Fatal("another provider could invoke the local tool")
	}
	result, err := s.call(context.Background(), s.sessions["codex"], raw)
	if err != nil || calls != 1 {
		t.Fatalf("local tool did not execute: %v %v", result, err)
	}
	refused = true
	result, err = s.call(context.Background(), s.sessions["codex"], raw)
	if err != nil || result.(map[string]any)["isError"] != true {
		t.Fatal("policy refusal appeared as a successful tool result")
	}
	s.Unregister("codex")
	if s.sessions["codex"] != nil {
		t.Fatal("ended Job retained its local tools")
	}
}
