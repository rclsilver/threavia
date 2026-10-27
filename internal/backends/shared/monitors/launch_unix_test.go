//go:build linux || darwin

package monitors

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The probe's error is what the backend logs when monitor_start is missing.
// An exit status alone leaves the operator guessing which requirement failed.
func TestProbeErrorSaysWhyTheSandboxCannotStart(t *testing.T) {
	reason := "bwrap: No permissions to create a new namespace"
	err := runProbe(exec.Command("/bin/sh", "-c", "echo '"+reason+"' >&2; exit 1"))
	if err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("the sandbox's own explanation was lost: %v", err)
	}
	if err = runProbe(exec.Command("/bin/sh", "-c", "exit 0")); err != nil {
		t.Fatalf("a sandbox that starts must not be reported as failing: %v", err)
	}
}

// Read-only and offline policies are promises only a sandbox keeps. Without
// one, the launcher must refuse them rather than run the command anyway.
func TestUnsandboxedLaunchRefusesLimitsItCannotEnforce(t *testing.T) {
	for _, limits := range [][2]bool{{false, true}, {true, false}, {false, false}} {
		cmd, err := unsandboxedCommand(t.Context(), t.TempDir(), "true", limits[0], limits[1])
		if err == nil || cmd != nil {
			t.Fatalf("writable=%v network=%v was launched without the isolation it requires", limits[0], limits[1])
		}
	}
}

// Without a PID namespace, the supervisor's process group is all that keeps a
// stopped watch from leaving its background children running on the host.
func TestUnsandboxedMonitorStopEndsEveryProcessItStarted(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(t.TempDir(), "monitors.json"), true)
	if m.Sandboxed() {
		t.Fatal("an unsandboxed manager claims isolation it does not provide")
	}
	s := snapshot("host")
	s.Command = "echo $$ > leader; sleep 60 & echo $! > descendant; wait"
	if err := m.Start(s, dir, true, true); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "host", "RUNNING")
	leader, _ := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "leader")))))
	descendant, _ := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "descendant")))))
	if err := m.Stop("host", "run"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "host", "STOPPED")
	assertProcessStopped(t, leader)
	assertProcessStopped(t, descendant)
}
