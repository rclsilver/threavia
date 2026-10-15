package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeAuthenticationProbeUsesOnlyCLIStatus(t *testing.T) {
	for _, tc := range []struct {
		output               string
		exit                 string
		authenticated, known bool
	}{
		{`{"loggedIn":true}`, "0", true, true},
		{`{"loggedIn":false}`, "1", false, true},
		{`unsupported command`, "1", false, false},
	} {
		binary := filepath.Join(t.TempDir(), "claude")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.output+"'\nexit "+tc.exit+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		authenticated, known := newRunner(t, binary).Authenticated(context.Background())
		if authenticated != tc.authenticated || known != tc.known {
			t.Fatalf("%s: %v %v", tc.output, authenticated, known)
		}
	}
}
