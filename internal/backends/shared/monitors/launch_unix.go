//go:build linux || darwin

package monitors

import (
	"fmt"
	"os/exec"
	"strings"
)

// runProbe keeps what the sandbox printed: a bare exit status says it failed,
// its stderr says why (a missing namespace permission, a refused /proc mount).
func runProbe(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if msg := strings.TrimSpace(string(out)); err != nil && msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
