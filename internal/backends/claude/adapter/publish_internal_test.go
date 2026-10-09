package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/policy"
)

// publishing builds an adapter running one Job in a fresh working directory,
// with a scratch directory beside it and the given refusals.
func publishing(t *testing.T, rules ...*backendv1.PermissionRule) (*Adapter, string, string) {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	scratch := filepath.Join(base, "scratch")
	for _, dir := range []string{work, scratch, filepath.Join(work, "secrets")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	p := policy.From(&backendv1.ExecutionPolicy{Rules: rules})
	p.Scratch = scratch
	a := &Adapter{jobs: map[string]*jobState{"job-1": {policy: p, workingDirectory: work}}}
	return a, work, scratch
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestPublishingReadsOnlyWhatTheJobMayRead pins the limits of a file an agent
// hands to Core: its own directories, after links are followed, and nothing a
// FILE_READ refusal covers.
func TestPublishingReadsOnlyWhatTheJobMayRead(t *testing.T) {
	t.Parallel()

	a, work, scratch := publishing(t, &backendv1.PermissionRule{
		Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
		Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ,
		Match:      "secrets/*",
	})
	write(t, filepath.Join(work, "report.html"), "<h1>Report</h1>")
	write(t, filepath.Join(scratch, "chart.svg"), "<svg/>")
	write(t, filepath.Join(work, "secrets", "token"), "hunter2")
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	write(t, outside, "not yours")
	if err := os.Symlink(outside, filepath.Join(work, "link.txt")); err != nil {
		t.Fatalf("linking: %v", err)
	}

	content, name, err := a.readPublishable("job-1", "report.html")
	if err != nil || name != "report.html" || string(content) != "<h1>Report</h1>" {
		t.Fatalf("a relative path in the working directory: got %q %q %v", name, content, err)
	}
	if _, _, err := a.readPublishable("job-1", filepath.Join(scratch, "chart.svg")); err != nil {
		t.Fatalf("the scratch directory must be publishable: %v", err)
	}

	for path, want := range map[string]string{
		outside:                  "working directory",
		"link.txt":               "working directory",
		"../scratch/../../x":     "cannot read",
		"secrets/token":          "refuses reading",
		"":                       "path is empty",
		filepath.Join(work, "."): "not a file",
	} {
		if _, _, err := a.readPublishable("job-1", path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("publishing %q: got %v, want an error mentioning %q", path, err, want)
		}
	}
}

// TestPublishingRefusesALargeFile keeps a file the size of a disk image off the
// control stream.
func TestPublishingRefusesALargeFile(t *testing.T) {
	t.Parallel()

	a, work, _ := publishing(t)
	big := filepath.Join(work, "big.bin")
	if err := os.WriteFile(big, make([]byte, maxPublishBytes+1), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, _, err := a.readPublishable("job-1", big); err == nil || !strings.Contains(err.Error(), "up to") {
		t.Fatalf("a file over the limit: got %v", err)
	}
}
