package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (s *Server) BeforeAction(ctx context.Context, jobID string, tools []CoreTool) error {
	enabled := false
	for _, tool := range tools {
		if tool.Name == "review_request" {
			enabled = true
			break
		}
	}
	if !enabled {
		return nil
	}
	if checker, ok := s.asker.(interface {
		ReviewCheckpoint(context.Context, string) error
	}); ok {
		return checker.ReviewCheckpoint(ctx, jobID)
	}
	return nil
}

// ReviewEndpoint is deliberately a job credential, never a public endpoint.
func (s *Server) ReviewEndpoint(jobID string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	for token, sess := range s.sessions {
		if sess.jobID == jobID {
			for _, tool := range sess.coreTools {
				if tool.Name == "review_request" {
					return fmt.Sprintf("http://%s/review/%s", s.listener.Addr(), token)
				}
			}
		}
	}
	return ""
}

func (s *Server) handleReviewCheckpoint(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFor(r.PathValue("token"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := s.BeforeAction(r.Context(), sess.jobID, sess.coreTools); err != nil {
		_ = json.NewEncoder(w).Encode(reviewHookDenial(err.Error()))
		return
	}
	// Empty success preserves Claude's ordinary permission evaluation. An allow
	// decision here would accidentally bypass the execution policy.
	_, _ = w.Write([]byte("{}"))
}

func reviewHookDenial(reason string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason}}
}

// RunReviewHook is a backend entry point, so no shell helper or external HTTP
// executable needs to be installed on backend machines.
func RunReviewHook(endpoint string, input io.Reader, output io.Writer) {
	deny := func() {
		_ = json.NewEncoder(output).Encode(reviewHookDenial("Threavia could not obtain the human review checkpoint. Wait for review approval before continuing."))
	}
	body, err := io.ReadAll(io.LimitReader(input, 8<<20))
	if err != nil || !json.Valid(body) {
		deny()
		return
	}
	response, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		deny()
		return
	}
	defer response.Body.Close()
	var value map[string]any
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value) != nil || value == nil {
		deny()
		return
	}
	_ = json.NewEncoder(output).Encode(value)
}
