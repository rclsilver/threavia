//go:build linux || darwin

package monitors

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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

// unsandboxedCommand runs a watch directly on the host, for a backend whose
// operator accepted it where the sandbox cannot start. The supervisor still
// owns the command's process group, so a stop, the deadline or the backend's
// death ends what it started; nothing limits what those processes reach. A
// policy that forbids writes or network access needs a sandbox to hold, so it
// is refused rather than silently weakened.
func unsandboxedCommand(ctx context.Context, dir, command string, writable, network bool) (*exec.Cmd, error) {
	if !writable || !network {
		return nil, errors.New("without a sandbox, a monitor cannot be kept from writing files or reaching the network")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return nil, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return supervisedCommand(ctx, supervisedProcess{Path: bash, Args: []string{"--noprofile", "--norc", "-c", command}, Dir: dir})
}
