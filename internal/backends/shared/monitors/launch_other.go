//go:build !linux && !darwin

package monitors

import (
	"context"
	"fmt"
	"os/exec"
)

func Available() bool { return false }
func sandboxCommand(context.Context, string, string, bool, bool) (*exec.Cmd, error) {
	return nil, fmt.Errorf("persistent monitors require Linux with bubblewrap or macOS with sandbox-exec; this backend cannot provide the required isolation")
}
