package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/rclsilver/threavia/internal/backends/shared/monitors"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
	"github.com/rclsilver/threavia/pkg/monitor"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestMonitorStateIsPrivateToEachBackendDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.StatePath = filepath.Join(dir, "claude.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := New(cfg, nil, state.NewMemoryStore(), logger)
	first.monitors.Fail(monitor.Snapshot{ID: "one", RunID: "run", JobID: "job"}, errors.New("stopped"))
	cfg.StatePath = filepath.Join(dir, "codex.db")
	second := New(cfg, nil, state.NewMemoryStore(), logger)
	values, err := second.monitors.Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatal("a backend imported another database's monitors")
	}
	cfg.StatePath = filepath.Join(dir, "claude.db")
	reopened := New(cfg, nil, state.NewMemoryStore(), logger)
	values, err = reopened.monitors.Snapshots()
	if err != nil || len(values) != 1 {
		t.Fatal("original backend lost monitor history", values, err)
	}
}

// When the sandbox cannot start, monitor_start silently disappears from every
// Job. The startup log is the one place that says why, and whether the
// operator's opt-in made monitors run on the host instead.
func TestBackendSaysWhyMonitorsLackTheirSandbox(t *testing.T) {
	reason := monitors.Probe()
	if reason == nil {
		t.Skip("the sandbox starts on this machine")
	}
	cfg := Default()
	cfg.StatePath = filepath.Join(t.TempDir(), "claude.db")
	for _, optIn := range []bool{false, true} {
		cfg.UnsandboxedMonitors = optIn
		var log bytes.Buffer
		a := New(cfg, nil, state.NewMemoryStore(), slog.New(slog.NewJSONHandler(&log, nil)))
		var entry struct{ Msg, Error string }
		if err := json.Unmarshal(log.Bytes(), &entry); err != nil || entry.Error != reason.Error() {
			t.Fatalf("the probe failure was not logged: %q", log.String())
		}
		if a.monitors.Sandboxed() == optIn {
			t.Fatalf("opt-in %v: sandboxed=%v", optIn, a.monitors.Sandboxed())
		}
		if optIn != strings.Contains(entry.Msg, "without a sandbox") {
			t.Fatalf("opt-in %v not reflected in the log: %q", optIn, entry.Msg)
		}
	}
}
