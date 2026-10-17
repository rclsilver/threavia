package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	c := &Codex{startupTimeout: time.Second, compactionTimeout: time.Second}
	live := &execution{hookReady: make(chan struct{})}
	start := func(context.Context) error { return nil }
	progress := func(string) { t.Error("unexpected compaction") }
	t.Run("confirmed", func(t *testing.T) {
		ready := &execution{hookReady: make(chan struct{})}
		close(ready.hookReady)
		if _, _, err := c.startFirstTurn(context.Background(), &rpc{}, ready, start, progress); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancelled without confirmation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := c.startFirstTurn(ctx, &rpc{}, live, start, progress); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("provider exit", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if _, _, err := c.startFirstTurn(context.Background(), &rpc{done: done, err: io.ErrUnexpectedEOF}, live, start, progress); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
	})
}

type compactingSink struct {
	*recordingSink
	endpoint *deferredHookEndpoint
	t        *testing.T
}

func (s compactingSink) Progress(ctx context.Context, run, job, kind, detail string) {
	s.recordingSink.Progress(ctx, run, job, kind, detail)
	if detail == "compacting context" {
		decision, err := s.endpoint.gate(ctx, map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "pwd"}})
		if err != nil || decision.(map[string]any)["decision"] != "block" {
			s.t.Error("compaction allowed a tool before SessionStart")
		}
	}
	if detail == "context compacted" {
		_, _ = s.endpoint.gate(ctx, map[string]any{"hook_event_name": "SessionStart"})
	}
}

func TestResumedStartupCompaction(t *testing.T) {
	for _, scenario := range []string{"startup-compact-before-response", "startup-compact-after-response"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, scenario)
			p.NativeSessionID = "thread-1"
			c.startupTimeout, c.compactionTimeout = 250*time.Millisecond, 2*time.Second
			endpoint := &deferredHookEndpoint{}
			c.tools = endpoint
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Run(ctx, p, compactingSink{sink, endpoint, t}); err != nil {
				t.Fatal(err)
			}
			if sink.code != "" || sink.summary != "done" {
				t.Fatalf("compaction failed: %s: %s", sink.code, sink.summary)
			}
			assertCompactionProgress(t, sink.events)
			if sink.payloads["progress:compacting context"]["kind"] != "compacting_context" || sink.payloads["progress:context compacted"]["kind"] != "context_compacted" {
				t.Fatalf("compaction is not visible to clients: %v", sink.payloads)
			}
		})
	}
}

func assertCompactionProgress(t *testing.T, events []string) {
	t.Helper()
	starts, finishes, startAt, finishAt := 0, 0, -1, -1
	for i, event := range events {
		switch event {
		case "progress:compacting context":
			starts++
			startAt = i
		case "progress:context compacted":
			finishes++
			finishAt = i
		}
	}
	if starts != 1 || finishes != 1 || startAt >= finishAt {
		t.Fatalf("missing, duplicated or reordered compaction progress: %v", events)
	}
}

func TestCompactionDuringEstablishedTurn(t *testing.T) {
	c, p, sink := provider(t, "compact")
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "" || sink.summary != "done" {
		t.Fatalf("compaction failed: %s: %s", sink.code, sink.summary)
	}
	assertCompactionProgress(t, sink.events)
}

func TestStartupCompactionFailureAndTimeout(t *testing.T) {
	for _, scenario := range []string{"startup-compaction-hang", "startup-compaction-error"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, scenario)
			p.NativeSessionID = "thread-1"
			c.startupTimeout, c.compactionTimeout = 250*time.Millisecond, 500*time.Millisecond
			c.tools = &deferredHookEndpoint{}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Run(ctx, p, sink); err != nil {
				t.Fatal(err)
			}
			if sink.code != "PROVIDER_STARTUP_FAILED" || !strings.Contains(sink.summary, "context compaction") {
				t.Fatalf("misleading compaction failure: %s: %s", sink.code, sink.summary)
			}
			if scenario == "startup-compaction-error" && !strings.Contains(sink.summary, "compaction request failed") {
				t.Fatalf("lost provider failure: %s", sink.summary)
			}
			if scenario == "startup-compaction-hang" && !strings.Contains(sink.summary, "deadline exceeded") {
				t.Fatalf("compaction wait was not bounded: %s", sink.summary)
			}
			for _, event := range sink.events {
				if event == "completed" || strings.HasPrefix(event, "tool:") {
					t.Fatalf("work processed without confirmation: %v", sink.events)
				}
			}
		})
	}
}

func TestCompactionCompletionDoesNotConfirmPolicy(t *testing.T) {
	c, p, sink := provider(t, "startup-compact-after-response")
	p.NativeSessionID = "thread-1"
	c.startupTimeout, c.compactionTimeout = 250*time.Millisecond, time.Second
	c.tools = &deferredHookEndpoint{}
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	assertCompactionProgress(t, sink.events)
	if sink.code != "PROVIDER_STARTUP_FAILED" || !strings.Contains(sink.summary, "policy hook handshake") {
		t.Fatalf("compaction replaced policy confirmation: %s: %s", sink.code, sink.summary)
	}
	for _, event := range sink.events {
		if event == "completed" || strings.HasPrefix(event, "tool:") {
			t.Fatalf("work processed without confirmation: %v", sink.events)
		}
	}
}

func TestStartupCompactionRespectsParentDeadline(t *testing.T) {
	c := &Codex{startupTimeout: time.Second, compactionTimeout: time.Minute}
	live := &execution{hookReady: make(chan struct{}), threadID: "root"}
	r := &rpc{events: make(chan message, 1)}
	r.events <- startupCompactionEvent("item/started", "root", "turn", "c")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := make(chan struct{})
	_, phase, err := c.startFirstTurn(ctx, r, live, func(ctx context.Context) error {
		defer close(joined)
		<-ctx.Done()
		return ctx.Err()
	}, func(string) { cancel() })
	if !errors.Is(err, context.Canceled) || phase != "context compaction" {
		t.Fatalf("compaction ignored cancellation: %s: %v", phase, err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("first turn was not reaped after cancellation")
	}
}

func TestStartupRefusesNativeApprovalBeforeConfirmation(t *testing.T) {
	c := &Codex{startupTimeout: 20 * time.Millisecond}
	live := &execution{hookReady: make(chan struct{}), threadID: "root"}
	var output bytes.Buffer
	r := &rpc{events: make(chan message, 1), writer: &output}
	r.events <- message{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"root","command":"touch example.txt"}`)}
	_, _, err := c.startFirstTurn(context.Background(), r, live, func(context.Context) error { return nil }, func(string) {})
	var response message
	if !errors.Is(err, context.DeadlineExceeded) || json.Unmarshal(output.Bytes(), &response) != nil || response.Error == nil || !strings.Contains(response.Error.Message, "policy hook handshake") {
		t.Fatalf("native approval escaped the startup gate: %s (%v)", output.String(), err)
	}
}

func startupCompactionEvent(method, thread, turn, id string) message {
	payload, _ := json.Marshal(map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"id": id, "type": "contextCompaction"}})
	return message{Method: method, Params: payload}
}

func TestStartupCompactionDoesNotRenewBudgetOrTrustOtherThreads(t *testing.T) {
	for _, scenario := range []string{"repeated compactions", "duplicate events", "foreign thread", "old turn"} {
		t.Run(scenario, func(t *testing.T) {
			c := &Codex{startupTimeout: 50 * time.Millisecond, compactionTimeout: 150 * time.Millisecond}
			live := &execution{hookReady: make(chan struct{}), threadID: "root", turnID: "turn"}
			r := &rpc{events: make(chan message), writer: &bytes.Buffer{}}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			thread, turn := "root", "turn"
			other := scenario == "foreign thread" || scenario == "old turn"
			if scenario == "foreign thread" {
				thread = "other"
			}
			if scenario == "old turn" {
				turn = "old"
			}
			go func() {
				for i := 0; ; i++ {
					id := "c"
					if scenario == "repeated compactions" {
						id = fmt.Sprint(i)
					}
					for _, event := range []message{startupCompactionEvent("item/started", thread, turn, id), startupCompactionEvent("item/completed", thread, turn, id)} {
						select {
						case r.events <- event:
						case <-ctx.Done():
							return
						}
					}
					select {
					case <-time.After(10 * time.Millisecond):
					case <-ctx.Done():
						return
					}
				}
			}()
			start := time.Now()
			var progress []string
			_, _, err := c.startFirstTurn(ctx, r, live, func(context.Context) error { return nil }, func(detail string) { progress = append(progress, detail) })
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
				t.Fatalf("unbounded startup: %v", err)
			}
			if other && len(progress) != 0 {
				t.Fatalf("foreign progress accepted: %v", progress)
			}
			if scenario == "duplicate events" && len(progress) != 2 {
				t.Fatalf("duplicate progress: %v", progress)
			}
		})
	}
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
