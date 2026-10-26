package monitors

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func darwinRun(t *testing.T, dir, command string, writable, network bool) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, err := sandboxCommand(ctx, dir, command, writable, network)
	if err != nil {
		t.Fatal(err)
	}
	return cmd.CombinedOutput()
}

func TestRealSeatbeltCapabilityAndFilesystem(t *testing.T) {
	// This must fail, rather than skip, on the native macOS CI runners if
	// sandbox-exec has disappeared or the profile is rejected by that OS.
	if !Available() {
		t.Fatal("the real macOS sandbox preflight failed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "workspace")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input"), []byte("readable"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := darwinRun(t, dir, `cat input; printf scratch > "$TMPDIR/temporary"; cat "$TMPDIR/temporary"`, false, false); err != nil || string(out) != "readablescratch" {
		t.Fatalf("read/scratch failed: %v %s", err, out)
	}
	if out, err := darwinRun(t, dir, "touch forbidden", false, false); err == nil {
		t.Fatalf("read-only workspace was writable: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("read-only workspace changed")
	}
	if out, err := darwinRun(t, dir, "touch allowed", true, false); err != nil {
		t.Fatalf("workspace write failed: %v %s", err, out)
	}
	// Symlinks must not turn a workspace write grant into a sibling grant.
	if err := os.Symlink(root, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"touch ../forbidden", "touch outside/forbidden"} {
		if out, err := darwinRun(t, dir, command, true, false); err == nil {
			t.Fatalf("write escaped workspace: %s %s", command, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("sibling changed")
	}
	// Canonicalization of macOS /var -> /private/var aliases must retain access.
	linked := filepath.Join(root, "alias")
	if err := os.Symlink(dir, linked); err != nil {
		t.Fatal(err)
	}
	if out, err := darwinRun(t, linked, "touch through-alias", true, false); err != nil {
		t.Fatalf("canonical workspace failed: %v %s", err, out)
	}
}

func TestRealSeatbeltNetworkPolicy(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	for _, allowed := range []bool{false, true} {
		out, err := darwinRun(t, t.TempDir(), fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", port), false, allowed)
		if allowed && err != nil {
			t.Fatalf("permitted loopback blocked: %v %s", err, out)
		}
		if !allowed && err == nil {
			t.Fatal("forbidden loopback succeeded")
		}
	}
	// A Unix-domain socket must not bypass the network refusal either.
	// Keep below Darwin's short Unix socket path limit, regardless of Go's TMPDIR.
	socketDir, err := os.MkdirTemp("/tmp", "threavia-monitor-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "service.sock")
	unixListener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer unixListener.Close()
	command := `exec /usr/bin/nc -w 1 -U ` + shellQuote(socket)
	if out, err := darwinRun(t, t.TempDir(), command, false, false); err == nil {
		t.Fatalf("forbidden Unix socket connected: %s", out)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestRealSeatbeltCannotSignalSupervisor(t *testing.T) {
	if out, err := darwinRun(t, t.TempDir(), `kill -TERM "$PPID"`, false, false); err == nil {
		t.Fatalf("sandbox signaled its trusted supervisor: %s", out)
	}
}

func TestRealSeatbeltMonitorSurvivesCallerAndStopsDescendants(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "monitors.json"))
	s := snapshot("mac-watch")
	s.Command = `echo $$; sleep 60 & echo $!; wait`
	if err := m.Start(s, dir, false, false); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID, s.RunID)
	// The originating provider/Job has no ownership of this command's context.
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	_ = caller
	until := time.Now().Add(10 * time.Second)
	var pids []int
	for time.Now().Before(until) {
		values, err := m.Snapshots()
		if err != nil {
			t.Fatal(err)
		}
		if len(values) != 1 || values[0].State != "RUNNING" {
			t.Fatalf("monitor stopped prematurely: %+v", values)
		}
		fields := strings.Fields(values[0].Output)
		if len(fields) == 2 {
			for _, field := range fields {
				pid, err := strconv.Atoi(field)
				if err != nil || pid <= 1 {
					t.Fatalf("invalid child PID: %q", field)
				}
				pids = append(pids, pid)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pids) != 2 {
		t.Fatal("sandbox did not start command and descendant")
	}
	if err := m.Stop(s.ID, s.RunID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, s.ID, "STOPPED")
	for _, pid := range pids {
		assertProcessStopped(t, pid)
	}
}

func TestSeatbeltBackendFixture(t *testing.T) {
	dir := os.Getenv("MONITOR_SEATBELT_FIXTURE")
	if dir == "" {
		return
	}
	m := New(filepath.Join(dir, "monitors.json"))
	s := snapshot("mac-backend-kill")
	s.Command = `echo $$ > leader; sleep 60 & echo $! > descendant; wait`
	if err := m.Start(s, dir, true, false); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID, s.RunID)
	time.Sleep(time.Hour)
}

func TestRealSeatbeltStopsAfterBackendSIGKILL(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backend := exec.Command(self, "-test.run=^TestSeatbeltBackendFixture$")
	backend.Env = append(os.Environ(), "MONITOR_SEATBELT_FIXTURE="+dir)
	backend.Stderr = os.Stderr
	if err = backend.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Process.Kill() })
	var pids []int
	for _, file := range []string{"leader", "descendant"} {
		pid, err := strconv.Atoi(strings.TrimSpace(string(waitFile(t, filepath.Join(dir, file)))))
		if err != nil || pid <= 1 {
			t.Fatal("invalid child PID")
		}
		pids = append(pids, pid)
	}
	if err = backend.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = backend.Wait()
	for _, pid := range pids {
		assertProcessStopped(t, pid)
	}
}

func TestRealSeatbeltTemporaryDirectoryIsCleaned(t *testing.T) {
	out, err := darwinRun(t, t.TempDir(), `printf '%s' "$TMPDIR"`, false, false)
	if err != nil {
		t.Fatalf("private temporary directory failed: %v %s", err, out)
	}
	temporary := string(out)
	if !strings.HasPrefix(filepath.Base(temporary), "threavia-monitor-") {
		t.Fatalf("not a private monitor temporary directory: %q", temporary)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("monitor temporary directory remained after normal exit: %v", err)
	}
}
