package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type startupTurnFailure struct{ failure map[string]any }

func (e *startupTurnFailure) Error() string {
	if detail := str(e.failure, "message"); detail != "" {
		return detail
	}
	return "Codex first turn failed before startup confirmation"
}

// Read lifecycle events even while turn/start or SessionStart is pending. A
// resumed thread can compact before invoking SessionStart. Native tool hooks
// still refuse work until confirmation; this loop never grants approvals.
func (c *Codex) startFirstTurn(ctx context.Context, r *rpc, live *execution, start func(context.Context) error, progress func(string)) ([]message, string, error) {
	callCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		result <- start(callCtx)
	}()
	defer func() { cancel(); <-callDone }()

	// Repeated starts and multiple compactions cannot renew this cap. The
	// parent context (including the Job's policy limit) also bounds the wait.
	limit := time.Now().Add(c.startupTimeout + c.compactionTimeout)
	timer := time.NewTimer(c.startupTimeout)
	defer timer.Stop()
	pulse := time.NewTicker(5 * time.Second)
	defer pulse.Stop()
	reset := func(next time.Time) {
		if next.After(limit) {
			next = limit
		}
		timer.Reset(time.Until(next))
	}
	var pending []message
	pendingBytes := 0
	active, finished := make(map[string]bool), make(map[string]bool)
	hook := live.hookReady
	rpcDone, exited := r.done, false
	confirmed, started := false, false
	phase := func() string {
		if len(active) > 0 {
			return "context compaction"
		}
		if !started {
			return "first turn"
		}
		return "policy hook handshake"
	}
	timeoutError := func() error {
		if len(active) > 0 {
			return fmt.Errorf("Codex context compaction did not finish within the startup budget; no tools were allowed before confirmation: %w", context.DeadlineExceeded)
		}
		return fmt.Errorf("Codex did not complete startup and confirm the Threavia policy hook; no tools were allowed before confirmation: %w", context.DeadlineExceeded)
	}
	observeHook := func() {
		select {
		case <-hook:
			confirmed, hook = true, nil
		default:
		}
	}
	observeResult := func() error {
		select {
		case err := <-result:
			if err != nil {
				return err
			}
			started, result = true, nil
		default:
		}
		return nil
	}
	for {
		// Prefer a confirmation/response already observed at the deadline.
		observeHook()
		if err := observeResult(); err != nil {
			return nil, phase(), err
		}
		if confirmed && started {
			return pending, "", nil
		}
		if !time.Now().Before(limit) {
			return nil, phase(), timeoutError()
		}
		if exited && result == nil && len(r.events) == 0 {
			return nil, phase(), fmt.Errorf("Codex exited during startup: %w", r.err)
		}
		select {
		case <-hook:
			confirmed, hook = true, nil
		case err := <-result:
			if err != nil {
				return nil, phase(), err
			}
			started, result = true, nil
		case <-ctx.Done():
			return nil, phase(), ctx.Err()
		case <-pulse.C:
			if len(active) > 0 {
				progress("compacting context")
			}
		case <-timer.C:
			observeHook()
			if err := observeResult(); err != nil {
				return nil, phase(), err
			}
			if confirmed && started {
				return pending, "", nil
			}
			return nil, phase(), timeoutError()
		case <-rpcDone:
			// Drain final notifications and observe the pending turn/start
			// response before classifying a process exit.
			rpcDone, exited = nil, true
		case msg := <-r.events:
			var payload map[string]any
			if len(msg.Params) > 0 {
				if err := json.Unmarshal(msg.Params, &payload); err != nil {
					return nil, phase(), fmt.Errorf("decode %s: %w", msg.Method, err)
				}
			}
			live.observeInitialInput(msg.Method, payload)
			live.mu.Lock()
			root, turn := live.threadID, live.turnID
			live.mu.Unlock()
			eventTurn := str(payload, "turnId")
			if msg.Method == "turn/completed" {
				ended, _ := payload["turn"].(map[string]any)
				eventTurn = str(ended, "id")
			}
			ours := root != "" && str(payload, "threadId") == root && (turn == "" || eventTurn == "" || eventTurn == turn)
			if len(msg.ID) == 0 && ours {
				if id, detail := compactionProgress(msg.Method, payload); detail != "" {
					if msg.Method == "item/started" {
						if !active[id] && !finished[id] {
							active[id] = true
							progress(detail)
							reset(limit)
						}
					} else if !finished[id] {
						wasActive := active[id]
						delete(active, id)
						finished[id] = true
						progress(detail)
						if wasActive && len(active) == 0 {
							reset(time.Now().Add(c.startupTimeout))
						}
					}
					continue
				}
				if msg.Method == "turn/completed" {
					turn, _ := payload["turn"].(map[string]any)
					if str(turn, "status") == "failed" {
						failure, _ := turn["error"].(map[string]any)
						return nil, phase(), &startupTurnFailure{failure}
					}
				}
			}
			if len(msg.ID) > 0 && !confirmed {
				if err := r.respond(msg.ID, nil, errors.New("Codex has not completed the policy hook handshake")); err != nil {
					return nil, phase(), err
				}
				continue
			}
			// Preserve other events, in order, for processing after confirmation.
			// Bound both count and bytes while draining the RPC pipe.
			pendingBytes += len(msg.Params)
			if len(pending) >= 256 || pendingBytes > 8<<20 {
				return nil, phase(), errors.New("too many Codex events before startup confirmation")
			}
			pending = append(pending, msg)
		}
	}
}

func compactionProgress(method string, payload map[string]any) (string, string) {
	item, _ := payload["item"].(map[string]any)
	if str(item, "type") != "contextCompaction" || str(item, "id") == "" {
		return "", ""
	}
	switch method {
	case "item/started":
		return str(item, "id"), "compacting context"
	case "item/completed":
		return str(item, "id"), "context compacted"
	default:
		return "", ""
	}
}
