package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestADiffIsServedFromTheTreesOfTheJob pins the on-demand diff of spec
// section 22: the change of one file between the start and the end of a Job,
// what was already uncommitted before it excluded, without touching the index
// the person works with.
func TestADiffIsServedFromTheTreesOfTheJob(t *testing.T) {
	t.Parallel()

	directory := newRepository(t)
	ctx := context.Background()
	// Already dirty before the Job, and staged: neither may leak into the
	// Job's diff, and the staging must survive the capture.
	write(t, directory, "README.md", "one\ntwo\nmine\n")
	stage := exec.Command("git", "add", "README.md")
	stage.Dir = directory
	if out, err := stage.CombinedOutput(); err != nil {
		t.Fatalf("staging: %v: %s", err, out)
	}
	stagedBefore, _ := git(ctx, directory, "diff", "--cached", "--name-only")

	before := Observe(ctx, directory)
	write(t, directory, "README.md", "one\nTWO\nmine\n")
	write(t, directory, "docs/new file[1].md", "hello\n")
	summary := Since(ctx, directory, before)

	if summary.Directory != directory || summary.BaseTree == "" || summary.HeadTree == "" {
		t.Fatalf("summary = %+v, want the directory and both trees", summary)
	}

	diff, err := DiffOf(ctx, directory, summary.BaseTree, summary.HeadTree, "README.md")
	if err != nil {
		t.Fatalf("diffing README.md: %v", err)
	}
	if !strings.Contains(diff.Text, "-two") || !strings.Contains(diff.Text, "+TWO") {
		t.Errorf("diff = %q, want the Job's change", diff.Text)
	}
	if strings.Contains(diff.Text, "+mine") {
		t.Errorf("diff = %q, the change made before the Job is not the Job's", diff.Text)
	}

	// A name with glob characters is a name.
	added, err := DiffOf(ctx, directory, summary.BaseTree, summary.HeadTree, "docs/new file[1].md")
	if err != nil || !strings.Contains(added.Text, "+hello") {
		t.Errorf("diff of an added file = %q, %v", added.Text, err)
	}

	if stagedAfter, _ := git(ctx, directory, "diff", "--cached", "--name-only"); stagedAfter != stagedBefore {
		t.Errorf("staged files went from %q to %q: the capture must leave the index alone", stagedBefore, stagedAfter)
	}

	if _, err := DiffOf(ctx, directory, "not-a-tree", summary.HeadTree, "README.md"); err == nil {
		t.Error("a diff against something that is not a tree id must be refused")
	}
}
