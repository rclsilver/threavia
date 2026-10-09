package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs git in a directory as a test author.
func gitIn(t *testing.T, directory string, args ...string) {
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

func TestStatusOfSaysWhereARepositoryStands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// An origin, a clone of it that works, and a second clone that pushes:
	// what the first one does not know until it fetches.
	origin := newRepository(t)
	work := filepath.Join(t.TempDir(), "work")
	gitIn(t, filepath.Dir(work), "clone", "--quiet", origin, work)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", origin, other)
	gitIn(t, origin, "checkout", "--quiet", "--detach")

	repo, err := StatusOf(ctx, work, false)
	if err != nil {
		t.Fatalf("status of a fresh clone: %v", err)
	}
	if !repo.Tracked || repo.Branch != "main" || repo.Upstream != "origin/main" || repo.Head == "" {
		t.Fatalf("a fresh clone: got %+v", repo)
	}
	if !repo.Clean() || repo.Ahead != 0 || repo.Behind != 0 || repo.UpstreamGone {
		t.Fatalf("a fresh clone is clean and level with origin: got %+v", repo)
	}

	// One commit here, one pushed from elsewhere, and work of every kind.
	write(t, work, "local.txt", "here\n")
	gitIn(t, work, "add", "local.txt")
	gitIn(t, work, "commit", "--quiet", "-m", "local")
	write(t, other, "remote.txt", "there\n")
	gitIn(t, other, "add", "remote.txt")
	gitIn(t, other, "commit", "--quiet", "-m", "remote")
	gitIn(t, other, "push", "--quiet", "origin", "main")
	write(t, work, "staged.txt", "staged\n")
	gitIn(t, work, "add", "staged.txt")
	write(t, work, "README.md", "changed\n")
	write(t, work, "new.txt", "untracked\n")

	repo, err = StatusOf(ctx, work, false)
	if err != nil {
		t.Fatalf("status without a fetch: %v", err)
	}
	if repo.Ahead != 1 || repo.Behind != 0 {
		t.Fatalf("without a fetch, the push from elsewhere is unknown: ahead %d behind %d", repo.Ahead, repo.Behind)
	}
	if repo.Staged != 1 || repo.Unstaged != 1 || repo.Untracked != 1 || repo.Clean() {
		t.Fatalf("one staged, one modified, one untracked: got %+v", repo)
	}

	repo, err = StatusOf(ctx, work, true)
	if err != nil || repo.FetchError != "" {
		t.Fatalf("status with a fetch: %v %q", err, repo.FetchError)
	}
	if repo.Ahead != 1 || repo.Behind != 1 || repo.FetchedAt.IsZero() {
		t.Fatalf("after a fetch, the push from elsewhere is known: got %+v", repo)
	}
	// The fetch moved the remote-tracking branch, and nothing of the work.
	if repo.Staged != 1 || repo.Unstaged != 1 || repo.Untracked != 1 {
		t.Fatalf("a fetch must not touch the work: got %+v", repo)
	}
}

func TestStatusOfTellsWhatItCannotRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	plain := t.TempDir()
	repo, err := StatusOf(ctx, plain, false)
	if err != nil || repo.Tracked {
		t.Fatalf("a directory outside git is said to be so: %+v %v", repo, err)
	}
	if _, err := StatusOf(ctx, filepath.Join(plain, "missing"), false); err == nil {
		t.Fatal("a directory that does not exist must be an error")
	}

	// A fetch from a remote that is not there fails, and says so, while the
	// rest is still the state as last known.
	directory := newRepository(t)
	gitIn(t, directory, "remote", "add", "origin", filepath.Join(plain, "nowhere"))
	repo, err = StatusOf(ctx, directory, true)
	if err != nil || repo.FetchError == "" || repo.Branch != "main" {
		t.Fatalf("a failed fetch: %+v %v", repo, err)
	}
}

func TestParseRepositoryReadsDetachedAndGone(t *testing.T) {
	t.Parallel()

	var detached Repository
	parseRepository("# branch.oid 0123456789abcdef\n# branch.head (detached)\n", &detached)
	if detached.Branch != "" || detached.Head != "0123456" {
		t.Fatalf("detached: got %+v", detached)
	}

	var gone Repository
	parseRepository("# branch.oid 0123456789abcdef\n# branch.head main\n# branch.upstream origin/old\nu UU N... 1 2 3 4 a b c conflict.txt\n", &gone)
	if !gone.UpstreamGone || gone.Upstream != "origin/old" || gone.Conflicted != 1 {
		t.Fatalf("an upstream that is gone, and a conflict: got %+v", gone)
	}
}
