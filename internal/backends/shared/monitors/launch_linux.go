package monitors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var availability struct {
	sync.Once
	ready bool
}

func Available() bool {
	availability.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd, err := sandboxCommand(ctx, "/", "true", false, false)
		if err == nil {
			availability.ready = cmd.Run() == nil
		}
	})
	return availability.ready
}
func sandboxCommand(ctx context.Context, dir, command string, writable, network bool) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("persistent monitors require bubblewrap: %w", err)
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
	args := []string{"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--chdir", dir}
	if !network {
		args = append(args, "--unshare-net")
	}
	if writable {
		args = append(args, "--bind", dir, dir)
	} else if dir == "/tmp" || strings.HasPrefix(dir, "/tmp/") {
		// The private /tmp hides host workspaces there; restore only the approved
		// directory, with its original read-only permission.
		args = append(args, "--ro-bind", dir, dir)
	}
	args = append(args, "--", bash, "--noprofile", "--norc", "-c", command)
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Dir = dir
	cmd.Env = monitorEnvironment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}
