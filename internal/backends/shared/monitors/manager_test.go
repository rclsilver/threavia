package monitors

import (
	"context"
	"encoding/json"
	"github.com/rclsilver/threavia/pkg/monitor"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := New(filepath.Join(t.TempDir(), "monitors.json"))
	m.launch = func(ctx context.Context, dir, command string, _, _ bool) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = dir
		cmd.WaitDelay = time.Second
		return cmd, nil
	}
	return m
}
func snapshot(id string) monitor.Snapshot {
	now := time.Now().UTC()
	return monitor.Snapshot{ID: id, RunID: "run", JobID: "job", Command: "exec sleep 30", Description: "test", StartedAt: now, ExpiresAt: now.Add(time.Minute)}
}
func waitState(t *testing.T, m *Manager, id, state string) monitor.Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		values, err := m.Snapshots()
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range values {
			if v.ID == id && v.State == state {
				return v
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("monitor %s did not reach %s", id, state)
	return monitor.Snapshot{}
}
func TestMonitorSurvivesCallerAndStopsOnlyChosenProcess(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()
	for _, id := range []string{"one", "two"} {
		if err := m.Start(snapshot(id), dir, false, false); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Stop(id, "run") })
	}
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	_ = caller
	if err := m.Stop("one", "foreign-run"); err == nil {
		t.Fatal("foreign run stopped monitor")
	}
	if err := m.Stop("one", "run"); err != nil {
		t.Fatal(err)
	}
	ended := waitState(t, m, "one", "STOPPED")
	if ended.Notify {
		t.Fatal("manual stop must not wake model")
	}
	waitState(t, m, "two", "RUNNING")
	m.Ack("one", "foreign-run", ended.Sequence)
	values, _ := m.Snapshots()
	if len(values) != 2 {
		t.Fatal("foreign ack removed monitor")
	}
	m.Ack("one", "run", ended.Sequence-1)
	values, _ = m.Snapshots()
	if len(values) != 2 {
		t.Fatal("old ack removed monitor")
	}
	m.Ack("one", "run", ended.Sequence)
	values, _ = m.Snapshots()
	if len(values) != 1 {
		t.Fatal("terminal ack did not remove local snapshot")
	}
}
func TestRestartNeverReexecutesAndRetainsTerminalOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "monitors.json")
	s := snapshot("one")
	s.State = "RUNNING"
	s.Sequence = 10
	s.Output = "last event"
	raw, _ := json.Marshal([]monitor.Snapshot{s})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := New(path)
	values, err := m.Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].State != "INTERRUPTED" || values[0].Sequence != 11 || values[0].Output != "last event" || !values[0].Notify {
		t.Fatalf("recovery=%+v", values)
	}
	if m.entries["one"].cmd != nil {
		t.Fatal("restart adopted or reran process")
	}
}
func TestOutputBoundedCoalescedAndExpiry(t *testing.T) {
	m := testManager(t)
	s := snapshot("one")
	s.ExpiresAt = time.Now().Add(100 * time.Millisecond)
	if err := m.Start(s, t.TempDir(), false, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	e := m.entries["one"]
	m.mu.Unlock()
	w := &outputWriter{m: m, e: e}
	_, _ = w.Write([]byte(strings.Repeat("é", monitor.MaxOutput)))
	values, err := m.Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(values[0].Output) > monitor.MaxOutput || values[0].Sequence != 1 || values[0].Notify {
		t.Fatal("output not bounded or woke before debounce")
	}
	m.mu.Lock()
	e.lastPublished = time.Now().Add(-31 * time.Second)
	m.mu.Unlock()
	values, err = m.Snapshots()
	if err != nil || values[0].Sequence != 2 || !values[0].Notify {
		t.Fatal("coalesced stdout did not become an event")
	}
	final := waitState(t, m, "one", "EXPIRED")
	if !final.Notify {
		t.Fatal("expiry did not notify")
	}
}
func TestUnreadableStateFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitors.json")
	_ = os.WriteFile(path, []byte("not JSON"), 0600)
	m := New(path)
	if _, err := m.Snapshots(); err == nil {
		t.Fatal("corrupt state ignored")
	}
	if err := m.Start(snapshot("one"), t.TempDir(), false, false); err == nil {
		t.Fatal("started without state")
	}
}
