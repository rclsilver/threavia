package runner

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type deferredHookEndpoint struct {
	testEndpoint
	gate func(context.Context, map[string]any) (any, error)
}

func (e *deferredHookEndpoint) RegisterPolicyGate(_ string, gate func(context.Context, map[string]any) (any, error)) {
	e.gate = gate
}

type slowBindingSink struct {
	*recordingSink
	bound func()
}

func (s slowBindingSink) NativeSessionBound(ctx context.Context, run, job, native string) error {
	if err := s.recordingSink.NativeSessionBound(ctx, run, job, native); err != nil {
		return err
	}
	s.bound()
	return nil
}

func TestStartupHookHasFreshBudgetAfterSetup(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "resume"}[resume], func(t *testing.T) {
			c, p, sink := provider(t, "success")
			if resume {
				p.NativeSessionID = "thread-1"
			}
			c.startupTimeout = time.Second
			endpoint := &deferredHookEndpoint{}
			c.tools = endpoint
			confirmed := make(chan struct{})
			wrapped := slowBindingSink{sink, func() {
				// The old setup deadline is definitely expired before turn/start.
				time.Sleep(c.startupTimeout + 10*time.Millisecond)
				go func() {
					time.Sleep(20 * time.Millisecond)
					_, _ = endpoint.gate(context.Background(), map[string]any{"hook_event_name": "SessionStart"})
					close(confirmed)
				}()
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Run(ctx, p, wrapped); err != nil {
				t.Fatal(err)
			}
			<-confirmed
			if sink.code != "" || sink.summary != "done" {
				t.Fatalf("delayed startup failed: %s: %s", sink.code, sink.summary)
			}
		})
	}
}

func TestStartupFailuresAreTechnicalAndBounded(t *testing.T) {
	for _, scenario := range []string{"startup-stalled-turn", "success"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, scenario)
			c.startupTimeout = time.Second
			c.tools = &deferredHookEndpoint{} // no SessionStart confirmation
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Run(ctx, p, sink); err != nil {
				t.Fatal(err)
			}
			phase := "first turn"
			if scenario == "success" {
				phase = "policy hook handshake"
			}
			if sink.code != "PROVIDER_STARTUP_FAILED" || !strings.Contains(sink.summary, phase) || !strings.Contains(sink.summary, "deadline exceeded") {
				t.Fatalf("missing technical startup diagnostic: %s: %s", sink.code, sink.summary)
			}
			for _, event := range sink.events {
				if event == "completed" || strings.HasPrefix(event, "tool:") {
					t.Fatalf("processed work without handshake: %v", sink.events)
				}
			}
		})
	}
}

func TestPolicyHookWait(t *testing.T) {
	t.Run("confirmation wins over expired deadline", func(t *testing.T) {
		ready := make(chan struct{})
		close(ready)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		for range 100 {
			if err := waitPolicyHook(ctx, ready, &rpc{}); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("cancelled without confirmation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := waitPolicyHook(ctx, make(chan struct{}), &rpc{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("provider exit", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if err := waitPolicyHook(context.Background(), make(chan struct{}), &rpc{done: done, err: io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
	})
}

func TestToolsRemainBlockedUntilPolicyHookConfirmation(t *testing.T) {
	c, p, _ := provider(t, "success")
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy, hookReady: make(chan struct{})}
	event := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "pwd"}}
	result, err := c.preTool(context.Background(), live, p.JobID, event)
	if err != nil || result.(map[string]any)["decision"] != "block" {
		t.Fatalf("tool ran before confirmation: %v %v", result, err)
	}
	_, _ = c.preTool(context.Background(), live, p.JobID, map[string]any{"hook_event_name": "SessionStart"})
	result, err = c.preTool(context.Background(), live, p.JobID, event)
	if err != nil || result.(map[string]any)["decision"] == "block" {
		t.Fatalf("confirmed hook blocked read: %v %v", result, err)
	}
}
