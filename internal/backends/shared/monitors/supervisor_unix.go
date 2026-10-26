//go:build linux || darwin

package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const supervisorFlag = "--threavia-monitor-supervisor-v1"

type supervisedProcess struct {
	Path      string
	Args      []string
	Dir       string
	Temporary string
	Deadline  time.Time
}

// A separate, trusted process owns the sandbox's process group. Its stdin is
// a pipe held only by the backend. EOF therefore also handles SIGKILL/crashes,
// for which a Go cleanup hook in the backend could never run.
func init() {
	if len(os.Args) != 3 || os.Args[1] != supervisorFlag {
		return
	}
	var spec supervisedProcess
	if err := json.Unmarshal([]byte(os.Args[2]), &spec); err != nil {
		fmt.Fprintln(os.Stderr, "invalid monitor supervisor request:", err)
		os.Exit(125)
	}
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		fmt.Fprintln(os.Stderr, "monitor supervisor requires a backend lifetime pipe")
		os.Exit(125)
	}
	if spec.Temporary != "" {
		// Only a directory created by the launcher can be a cleanup target.
		if !filepath.IsAbs(spec.Temporary) || !strings.HasPrefix(filepath.Base(spec.Temporary), "threavia-monitor-") || filepath.Base(spec.Temporary) == "threavia-monitor-" {
			os.Exit(125)
		}
	}
	code := runSupervised(spec, os.Stdin)
	// os.Exit does not run defers.
	if spec.Temporary != "" {
		_ = os.RemoveAll(spec.Temporary)
	}
	os.Exit(code)
}

func supervisedCommand(ctx context.Context, spec supervisedProcess) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok && (spec.Deadline.IsZero() || deadline.Before(spec.Deadline)) {
		spec.Deadline = deadline
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	input, lifetime, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, self, supervisorFlag, string(raw))
	cmd.Env = monitorEnvironment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = input
	cmd.Cancel = func() error { return lifetime.Close() }
	cmd.WaitDelay = 5 * time.Second
	go func() {
		<-ctx.Done()
		_ = lifetime.Close()
		_ = input.Close()
		if spec.Temporary != "" {
			_ = os.RemoveAll(spec.Temporary)
		}
	}()
	return cmd, nil
}

func runSupervised(spec supervisedProcess, lifetime io.Reader) int {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)
	if !spec.Deadline.IsZero() && !time.Now().Before(spec.Deadline) {
		return 1
	}
	child := exec.Command(spec.Path, spec.Args...)
	child.Dir = spec.Dir
	child.Env = monitorEnvironment()
	if spec.Temporary != "" {
		child.Env = append(child.Env, "TMPDIR="+spec.Temporary, "TMP="+spec.Temporary, "TEMP="+spec.Temporary)
	}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	// Deliberately do not pass the lifetime pipe to untrusted commands.
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	kill := func() { _ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL) }
	defer kill()
	lost := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, lifetime); close(lost) }()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	var expires <-chan time.Time
	if !spec.Deadline.IsZero() {
		timer := time.NewTimer(time.Until(spec.Deadline))
		defer timer.Stop()
		expires = timer.C
	}
	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return 1
	case <-lost:
	case <-signals:
	case <-expires:
	}
	kill()
	<-done
	return 1
}
