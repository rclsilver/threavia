package monitors

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
)

func TestRealSandboxNetworkCannotEscapeItsNamespace(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	for _, network := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		cmd, err := sandboxCommand(ctx, t.TempDir(), fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", port), false, network)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		cancel()
		if network && err != nil {
			t.Fatalf("permitted network failed: %v %s", err, out)
		}
		if !network && err == nil {
			t.Fatal("isolated monitor reached host loopback")
		}
	}
}
