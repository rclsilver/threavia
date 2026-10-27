//go:build linux || darwin

package monitors

import (
	"os/exec"
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
