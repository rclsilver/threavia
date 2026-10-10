package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	skillbundle "github.com/rclsilver/threavia/internal/backends/shared/skills"
)

func TestMCPFailureIsReportedAsFailure(t *testing.T) {
	c, p, sink := provider(t, "mcp-failure")
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, event := range sink.events {
		if event == "result:WebFetch:web-1" {
			t.Fatal("refused web tool reported success")
		}
		failed = failed || event == "tool-failed:WebFetch:web-1"
	}
	if !failed {
		t.Fatal("MCP failure was not forwarded")
	}
}

func activeProvider(t *testing.T, scenario string) (*Codex, *execution, *recordingSink) {
	t.Helper()
	c, p, sink := provider(t, scenario)
	if strings.HasPrefix(scenario, "supervised") {
		p.Policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED
		p.Policy.Supervision = "Only perform the user's requested task."
	}
	if scenario == "supervised-readonly" {
		p.Policy.AllowFilesystemWrite = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, p, sink) }()
	t.Cleanup(func() {
		_ = c.Cancel(p.JobID)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("provider did not stop")
		}
	})
	for {
		c.mu.Lock()
		live := c.running[p.JobID]
		c.mu.Unlock()
		if live != nil {
			live.mu.Lock()
			started := live.turnID != ""
			live.mu.Unlock()
			if started {
				return c, live, sink
			}
		}
		select {
		case err := <-done:
			t.Fatalf("provider stopped before starting: %v (%s)", err, sink.summary)
		case <-ctx.Done():
			t.Fatal("provider did not start")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestNativeSkillsAndExplicitInvocation(t *testing.T) {
	c, p, sink := provider(t, "skills")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: review-code\ndescription: Review code when asked.\n---\nCheck correctness."), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	p.SkillDirectory, err = skillbundle.Assemble(filepath.Join(t.TempDir(), "job"), []skillbundle.Entry{{Name: "review-code", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	p.Prompt = "$review-code review the changes"
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "" {
		t.Fatalf("native skills failed: %s", sink.summary)
	}
}

func TestAutomaticReviewForWebAndExplicitAsk(t *testing.T) {
	for _, verdict := range []string{"allow", "deny", "ask"} {
		t.Run(verdict, func(t *testing.T) {
			c, live, sink := activeProvider(t, "supervised-"+verdict)
			asker := c.asker.(*testAsker)
			d, err := c.decide(context.Background(), live, "job", "WebSearch", map[string]any{"query": "look up the documentation"}, false)
			if err != nil || d.Approved != (verdict != "deny") {
				t.Fatalf("review decision: %+v %v", d, err)
			}
			asker.mu.Lock()
			count := len(asker.calls)
			asker.mu.Unlock()
			want := 0
			if verdict == "ask" {
				want = 1
			}
			if count != want {
				t.Fatalf("human prompts: got %d want %d", count, want)
			}
			live.mu.Lock()
			live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "WebSearch"}}
			live.mu.Unlock()
			_, _ = c.decide(context.Background(), live, "job", "WebSearch", map[string]any{"query": "explicit ask"}, false)
			asker.mu.Lock()
			count = len(asker.calls)
			asker.mu.Unlock()
			if count != want+1 {
				t.Fatal("automatic reviewer bypassed an explicit human ASK")
			}
			live.mu.Lock()
			helperCount := len(live.helpers)
			live.mu.Unlock()
			if helperCount != 0 {
				t.Fatal("helper thread leaked")
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			for _, event := range sink.events {
				if strings.HasPrefix(event, "message:") {
					t.Fatal("reviewer response leaked into the user conversation")
				}
			}
		})
	}
}

func TestSearchRequiresRealSearchAndFiltersDeniedDomains(t *testing.T) {
	for _, scenario := range []string{"helper-search", "helper-no-search", "helper-malformed"} {
		t.Run(scenario, func(t *testing.T) {
			c, live, _ := activeProvider(t, scenario)
			live.mu.Lock()
			live.policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
			live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK, Match: "denied.example"}}
			live.mu.Unlock()
			result, err := c.webSearch(context.Background(), live, "job", map[string]any{"query": "documentation"})
			if scenario != "helper-search" {
				if err == nil {
					t.Fatal("unverified results accepted")
				}
				return
			}
			if err != nil || !strings.Contains(result, "https://example.com/docs") || strings.Contains(result, "denied.example") {
				t.Fatalf("search results: %s %v", result, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebFetchChecksRedirectBeforeRequest(t *testing.T) {
	c, p, _ := provider(t, "success")
	live := &execution{cwd: p.WorkingDirectory, policy: p.Policy}
	live.policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK, Match: "denied.example"}}
	requests := 0
	c.webTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Hostname() == "denied.example" {
			t.Fatal("refused redirect made a network request")
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://denied.example/private"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: r}, nil
	})
	if _, err := c.webFetch(context.Background(), live, "job", map[string]any{"url": "https://example.com"}); err == nil {
		t.Fatal("refused redirect was accepted")
	}
	if requests != 1 {
		t.Fatalf("made %d requests", requests)
	}
}

func TestWebFetchAskAllowDenyAndHTML(t *testing.T) {
	for _, effect := range []backendv1.PermissionEffect{backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, backendv1.PermissionEffect_PERMISSION_EFFECT_DENY} {
		t.Run(fmt.Sprint(effect), func(t *testing.T) {
			c, p, _ := provider(t, "success")
			live := &execution{cwd: p.WorkingDirectory, policy: p.Policy}
			live.policy.Rules = []policy.Rule{{Effect: effect, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "mcp__threavia__web_fetch"}}
			calls := 0
			c.webTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader("<script>secret script</script><h1>Useful content</h1>")), Request: r}, nil
			})
			result, err := c.webFetch(context.Background(), live, "job", map[string]any{"url": "https://example.com"})
			if effect == backendv1.PermissionEffect_PERMISSION_EFFECT_DENY {
				if err == nil || calls != 0 {
					t.Fatal("denied fetch performed work")
				}
				return
			}
			if err != nil || !strings.Contains(result, "Useful content") || strings.Contains(result, "secret script") {
				t.Fatalf("page: %s %v", result, err)
			}
			asker := c.asker.(*testAsker)
			want := 0
			if effect == backendv1.PermissionEffect_PERMISSION_EFFECT_ASK {
				want = 1
			}
			if len(asker.calls) != want {
				t.Fatalf("human prompts: %v", asker.calls)
			}
		})
	}
}

func TestNativeRulesStillRequireHostApproval(t *testing.T) {
	c, p, _ := provider(t, "success")
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy, nativeRules: true}
	for _, command := range []string{"pwd", "git status", "touch example.txt"} {
		d, err := c.decide(context.Background(), live, "job", "Bash", map[string]any{"command": command}, true)
		if err != nil || !d.Approved {
			t.Fatalf("%s: %+v %v", command, d, err)
		}
	}
	if len(c.asker.(*testAsker).calls) != 1 {
		t.Fatal("a native allow could skip host approval, or ordinary reads asked")
	}
}

func TestSupervisedReadOnlyAndPolicyUpdate(t *testing.T) {
	_, _, _ = activeProvider(t, "supervised-readonly")
	c, live, _ := activeProvider(t, "supervised-allow")
	live.mu.Lock()
	updated := live.policy
	oldTurn := live.turnID
	live.mu.Unlock()
	updated.Mode = backendv1.ExecutionMode_EXECUTION_MODE_GUARDED
	c.UpdatePolicy("job", updated)
	deadline := time.After(3 * time.Second)
	for {
		live.mu.Lock()
		turn := live.turnID
		live.mu.Unlock()
		if turn != oldTurn {
			break
		}
		select {
		case <-deadline:
			t.Fatal("policy update did not interrupt the pending automatic turn")
		case <-time.After(time.Millisecond):
		}
	}
}
