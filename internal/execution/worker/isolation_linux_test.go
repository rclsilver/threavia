//go:build linux

package worker

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMonitorIsolationHelper(t *testing.T) {
	if os.Getenv("THREAVIA_TEST_MONITOR_HELPER") == "1" {
		err := monitorCommand([]string{os.Getenv("THREAVIA_TEST_MONITOR_MODE"), os.Getenv("THREAVIA_TEST_MONITOR_COMMAND")})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}
func TestMonitorIsolationProbe(t *testing.T) {
	if os.Getenv("EXECUTION_ISOLATION_PROBE") != "1" {
		return
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if os.Getenv("EXECUTION_EXPECT_NETWORK") == "1" {
		if err != nil {
			t.Fatal(err)
		}
	} else if err != unix.EPERM {
		t.Fatalf("offline command opened network socket: %v", err)
	}
	if fd >= 0 {
		unix.Close(fd)
	}
	fd, err = unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("local IPC blocked: %v", err)
	}
	unix.Close(fd)
	parent := os.Getenv("EXECUTION_TRUSTED_PID")
	if _, err = os.ReadFile("/proc/" + parent + "/environ"); err == nil {
		t.Fatal("command read trusted worker environment")
	}
	if _, err = os.Readlink("/proc/" + parent + "/fd/0"); err == nil {
		t.Fatal("command inspected trusted descriptors")
	}
}
func TestMonitorIsolationSupervisor(t *testing.T) {
	if os.Getenv("EXECUTION_ISOLATION_SUPERVISOR") != "1" {
		return
	}
	if err := harden(); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "' -test.run '^TestMonitorIsolationProbe$'"
	cmd := exec.Command(binary, "-test.run", "^TestMonitorIsolationHelper$")
	cmd.Env = append(os.Environ(), "THREAVIA_TEST_MONITOR_HELPER=1", "THREAVIA_TEST_MONITOR_COMMAND="+command, "EXECUTION_ISOLATION_PROBE=1", "EXECUTION_TRUSTED_PID="+strconv.Itoa(os.Getpid()))
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolation probe: %v\n%s", err, raw)
	}
}
func TestMonitorChildNetworkAndProcfsBoundary(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatal("bash required for monitor tests")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"offline", "network"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(binary, "-test.run", "^TestMonitorIsolationSupervisor$")
			allow := "0"
			if mode == "network" {
				allow = "1"
			}
			cmd.Env = append(os.Environ(), "EXECUTION_ISOLATION_SUPERVISOR=1", "THREAVIA_TEST_MONITOR_MODE="+mode, "EXECUTION_EXPECT_NETWORK="+allow)
			if raw, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("supervisor: %v\n%s", err, raw)
			}
		})
	}
}
