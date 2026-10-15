package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexAuthenticationProbeDoesNotStartAThread(t *testing.T) {
	for _, tc := range []struct {
		account       string
		authenticated bool
	}{{"null", false}, {`{"type":"chatgpt"}`, true}} {
		binary := filepath.Join(t.TempDir(), "codex")
		script := "#!/bin/sh\nIFS= read -r initialize\nprintf '%s\\n' '{\"id\":\"threavia-1\",\"result\":{}}'\nIFS= read -r initialized\nIFS= read -r account\nprintf '%s\\n' '{\"id\":\"threavia-2\",\"result\":{\"requiresOpenaiAuth\":true,\"account\":" + tc.account + "}}'\ncat >/dev/null\n"
		if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		c := New(binary, nil, Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		authenticated, known := c.Authenticated(ctx)
		cancel()
		if !known || authenticated != tc.authenticated {
			t.Fatalf("unexpected auth status: %v %v", authenticated, known)
		}
	}
}
