package monitors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var availability struct {
	sync.Once
	ready bool
}

func Available() bool {
	availability.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd, err := sandboxCommand(ctx, "/", "true", false, false)
		if err == nil {
			availability.ready = cmd.Run() == nil
		}
	})
	return availability.ready
}

func sandboxCommand(ctx context.Context, dir, command string, writable, network bool) (*exec.Cmd, error) {
	// Use the OS binary, never an executable selected through the project PATH.
	const sandbox = "/usr/bin/sandbox-exec"
	if _, err := os.Stat(sandbox); err != nil {
		return nil, fmt.Errorf("persistent monitors require macOS sandbox-exec: %w", err)
	}
	cwd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp("", "threavia-monitor-")
	if err != nil {
		return nil, err
	}
	temporary, err = canonicalTemporary(temporary)
	if err != nil {
		return nil, err
	}
	profile, err := seatbeltProfile(cwd, temporary, writable, network)
	if err != nil {
		_ = os.RemoveAll(temporary)
		return nil, err
	}
	cmd, err := supervisedCommand(ctx, supervisedProcess{
		Path: sandbox, Args: []string{"-p", profile, "/bin/bash", "--noprofile", "--norc", "-c", command}, Dir: cwd, Temporary: temporary,
	})
	if err != nil {
		_ = os.RemoveAll(temporary)
	}
	return cmd, err
}

func canonicalTemporary(dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return resolved, nil
}
