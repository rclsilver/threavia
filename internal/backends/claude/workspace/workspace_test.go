package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepository builds a real git repository with one committed file. The
// detector reads git, so a fake would only pin the fake.
func newRepository(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the workspace change tests")
	}

	directory := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = directory
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=threavia", "GIT_AUTHOR_EMAIL=threavia@example.invalid",
			"GIT_COMMITTER_NAME=threavia", "GIT_COMMITTER_EMAIL=threavia@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init", "--initial-branch", "main")
	write(t, directory, "README.md", "one\ntwo\n")
	run("add", ".")
	run("commit", "-m", "initial")
	return directory
}

func write(t *testing.T, directory, name, content string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestSinceReportsWhatChanged(t *testing.T) {
	t.Parallel()

	directory := newRepository(t)
	ctx := context.Background()

	before := Observe(ctx, directory)
	if !before.tracked {
		t.Fatal("a git repository must be observable")
	}

	write(t, directory, "README.md", "one\ntwo\nthree\n")
	write(t, directory, "docs/new.md", "fresh\n")

	summary := Since(ctx, directory, before)
	if summary.Empty() {
		t.Fatal("a modified and an added file must produce a summary")
	}

	states := make(map[string]State, len(summary.Files))
	for _, file := range summary.Files {
		states[file.Path] = file.State
	}
	if states["README.md"] != StateModified {
		t.Fatalf("README.md = %q, want MODIFIED (%+v)", states["README.md"], summary.Files)
	}
	if states["docs/new.md"] != StateAdded {
		t.Fatalf("docs/new.md = %q, want ADDED (%+v)", states["docs/new.md"], summary.Files)
	}
	if summary.Additions != 1 {
		t.Fatalf("additions = %d, want the single added line", summary.Additions)
	}
}

// TestSinceIgnoresWhatWasAlreadyDirty pins the point of bracketing a Job with
// two snapshots: a working directory that was already modified is not reported
// as the agent's doing.
func TestSinceIgnoresWhatWasAlreadyDirty(t *testing.T) {
	t.Parallel()

	directory := newRepository(t)
	ctx := context.Background()

	write(t, directory, "untouched.txt", "already here\n")
	before := Observe(ctx, directory)

	summary := Since(ctx, directory, before)
	if !summary.Empty() {
		t.Fatalf("nothing changed during the job, got %+v", summary)
	}
}

func TestSinceReportsADeletion(t *testing.T) {
	t.Parallel()

	directory := newRepository(t)
	ctx := context.Background()

	before := Observe(ctx, directory)
	if err := os.Remove(filepath.Join(directory, "README.md")); err != nil {
		t.Fatalf("removing the file: %v", err)
	}

	summary := Since(ctx, directory, before)
	if len(summary.Files) != 1 || summary.Files[0].State != StateDeleted {
		t.Fatalf("summary = %+v, want a single deletion", summary.Files)
	}
	if summary.Deletions != 2 {
		t.Fatalf("deletions = %d, want the two lines the file held", summary.Deletions)
	}
}

// TestSinceSaysNothingOutsideARepository pins that a directory git cannot read
// produces no summary at all, rather than a guess built from modification times.
func TestSinceSaysNothingOutsideARepository(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	ctx := context.Background()

	before := Observe(ctx, directory)
	if before.tracked {
		t.Fatal("a plain directory must not be observable")
	}

	write(t, directory, "file.txt", "content\n")
	if summary := Since(ctx, directory, before); !summary.Empty() {
		t.Fatalf("summary = %+v, want nothing outside a repository", summary)
	}
}

func TestParseStatusConsumesRenameRecords(t *testing.T) {
	t.Parallel()

	// A rename emits "R  new" followed by the original path in its own record.
	states := parseStatus("R  new/path.txt\x00old/path.txt\x00 M other.txt\x00")
	if states["new/path.txt"] != StateRenamed {
		t.Fatalf("new/path.txt = %q, want RENAMED", states["new/path.txt"])
	}
	if _, reported := states["old/path.txt"]; reported {
		t.Fatal("the original path of a rename must not be reported as its own change")
	}
	if states["other.txt"] != StateModified {
		t.Fatalf("other.txt = %q, want MODIFIED", states["other.txt"])
	}
}

func TestParseNumstatSkipsBinaryFiles(t *testing.T) {
	t.Parallel()

	counts := parseNumstat("12\t3\tmain.go\n-\t-\timage.png\n")
	if counts["main.go"].additions != 12 || counts["main.go"].deletions != 3 {
		t.Fatalf("main.go = %+v, want 12 additions and 3 deletions", counts["main.go"])
	}
	if _, reported := counts["image.png"]; reported {
		t.Fatal("a binary file has no line counts to report")
	}
}
