package monitors

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxCannotWidenNetworkOrMounts(t *testing.T) {
	dir := t.TempDir()
	// Inspect a fake launcher's arguments without executing it.
	binary := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd, err := sandboxCommand(context.Background(), dir, "echo test", false, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--unshare-net") || !strings.Contains(joined, "--ro-bind / /") || strings.Contains(joined, " --bind ") {
		t.Fatalf("unsafe isolation: %s", joined)
	}
	for _, v := range cmd.Env {
		if strings.HasPrefix(v, "THREAVIA_") {
			t.Fatal("backend secret env inherited")
		}
	}
}
func TestRealSandboxReadonlyAndNetworkIsolation(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	probe, err := sandboxCommand(context.Background(), t.TempDir(), "true", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("bubblewrap cannot initialize: %v: %s", err, out)
	}
	dir := t.TempDir()
	cmd, err := sandboxCommand(context.Background(), dir, "touch forbidden", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Run(); err == nil {
		t.Fatal("read-only monitor wrote to workspace")
	}
	if _, err = os.Stat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("unexpected write")
	}
	cmd, err = sandboxCommand(context.Background(), dir, "touch allowed", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("writable workspace failed: %v %s", err, out)
	}
}
