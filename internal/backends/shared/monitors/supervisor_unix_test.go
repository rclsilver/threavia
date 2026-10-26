//go:build linux || darwin

package monitors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitFile(t *testing.T, path string) []byte {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return raw
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("file was not produced:", path)
	return nil
}

func assertProcessStopped(t *testing.T, pid int) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		// A container PID 1 need not reap orphan zombies, which cannot do work.
		if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			if end := strings.LastIndex(string(raw), ") "); end >= 0 && strings.HasPrefix(string(raw)[end+2:], "Z ") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("process %d survived its supervisor", pid)
}

func TestSupervisorNormalExitAndCancellation(t *testing.T) {
	for _, code := range []int{0, 7} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd, err := supervisedCommand(ctx, supervisedProcess{Path: "/bin/sh", Args: []string{"-c", fmt.Sprintf("exit %d", code)}})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		err = cmd.Run()
		cancel()
		if code == 0 && err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != code {
				t.Fatalf("exit error %v", err)
			}
		}
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := supervisedCommand(ctx, supervisedProcess{Path: "/bin/sh", Dir: dir, Args: []string{"-c", "echo $$ > leader; sleep 60 & echo $! > descendant; wait"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	leader, _ := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "leader")))))
	descendant, _ := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "descendant")))))
	cancel()
	_ = cmd.Wait()
	assertProcessStopped(t, leader)
	assertProcessStopped(t, descendant)
}

func TestSupervisorBackendFixture(t *testing.T) {
	dir := os.Getenv("MONITOR_BACKEND_FIXTURE")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := supervisedCommand(ctx, supervisedProcess{Path: "/bin/sh", Dir: dir, Args: []string{"-c", "echo $$ > leader; sleep 60 & echo $! > descendant; wait"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	time.Sleep(time.Hour)
}

func TestSupervisorStopsChildrenAfterBackendSIGKILL(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backend := exec.Command(self, "-test.run=^TestSupervisorBackendFixture$")
	backend.Env = append(os.Environ(), "MONITOR_BACKEND_FIXTURE="+dir)
	backend.Stderr = os.Stderr
	if err = backend.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Process.Kill() })
	leader, _ := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "leader")))))
	descendant, _ := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "descendant")))))
	if err = backend.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = backend.Wait()
	assertProcessStopped(t, leader)
	assertProcessStopped(t, descendant)
}

func TestSupervisorCleansBackgroundChildrenOnNormalCompletion(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, err := supervisedCommand(ctx, supervisedProcess{Path: "/bin/sh", Dir: dir, Args: []string{"-c", "sleep 60 & echo $! > descendant; exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "descendant")))))
	if err != nil || pid <= 1 {
		t.Fatal("invalid descendant PID")
	}
	assertProcessStopped(t, pid)
}

func TestSupervisorEnforcesItsOwnDeadline(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, err := supervisedCommand(ctx, supervisedProcess{Path: "/bin/sh", Dir: dir, Args: []string{"-c", "echo $$ > leader; exec sleep 60"}, Deadline: time.Now().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	// supervisedCommand also caps this deadline by the context deadline, so use
	// a shorter requested expiry and ensure it is retained.
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, "leader")))))
	if err != nil || pid <= 1 {
		t.Fatal("invalid leader PID")
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("deadline did not terminate command")
	}
	assertProcessStopped(t, pid)
}
