package workspace

import (
	"context"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewTracksFurtherEditsToAnAlreadyDirtyFileAndCommits(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)
	write(t, repo, "README.md", "preexisting\ntwo\n")
	indexPath, err := git(ctx, repo, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		t.Fatal(err)
	}
	indexBefore, err := os.ReadFile(strings.TrimSpace(indexPath))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := CaptureReview(ctx, repo, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "README.md", "preexisting\nagent edit\n")
	_, err = gitWith(ctx, repo, []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}, "commit", "-am", "agent edit")
	if err != nil {
		t.Fatal(err)
	}
	current, err := CaptureReview(ctx, repo, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReviewFiles(ctx, current["gitDirectory"].(string), baseline["tree"].(string), current["tree"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0]["path"] != "README.md" {
		t.Fatalf("committed edit missing: %v", files)
	}
	content, err := ReviewContent(ctx, current["gitDirectory"].(string), baseline["tree"].(string), current["tree"].(string), "README.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if content["before"] != "preexisting\ntwo\n" || content["after"] != "preexisting\nagent edit\n" {
		t.Fatalf("baseline absorbed unrelated edits: %v", content)
	}
	// Capture itself never stages files in the real index.
	beforeCapture, err := os.ReadFile(strings.TrimSpace(indexPath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = CaptureReview(ctx, repo, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	afterCapture, _ := os.ReadFile(strings.TrimSpace(indexPath))
	if string(beforeCapture) != string(afterCapture) {
		t.Fatal("capture modified real index")
	}
	if len(indexBefore) == 0 {
		t.Fatal("missing initial index")
	}
	// Protected tree refs remain readable even under immediate object pruning.
	if _, err = git(ctx, repo, "gc", "--prune=now"); err != nil {
		t.Fatal(err)
	}
	if _, err = ReviewFiles(ctx, current["gitDirectory"].(string), baseline["tree"].(string), current["tree"].(string)); err != nil {
		t.Fatal(err)
	}
}

func TestReviewCapturesUnbornRepositoriesAndUntrackedFiles(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	if _, err := git(ctx, repo, "init", "--initial-branch", "main"); err != nil {
		t.Fatal(err)
	}
	baseline, err := CaptureReview(ctx, repo, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "new.go", "package example\n")
	current, err := CaptureReview(ctx, repo, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReviewFiles(ctx, current["gitDirectory"].(string), baseline["tree"].(string), current["tree"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0]["state"] != "ADDED" {
		t.Fatalf("untracked file absent: %v", files)
	}
}

func TestReviewRenamesSymlinksAndRemovedWorktrees(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if _, err := git(ctx, repo, "worktree", "add", "-b", "linked", linked); err != nil {
		t.Fatal(err)
	}
	baseline, err := CaptureReview(ctx, linked, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(linked, "README.md"), filepath.Join(linked, "renamed.md")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(linked, "link")); err != nil {
		t.Fatal(err)
	}
	current, err := CaptureReview(ctx, linked, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = git(ctx, repo, "worktree", "remove", "--force", linked); err != nil {
		t.Fatal(err)
	}
	common := current["gitDirectory"].(string)
	base := baseline["tree"].(string)
	head := current["tree"].(string)
	files, err := ReviewFiles(ctx, common, base, head)
	if err != nil {
		t.Fatal(err)
	}
	renamed := false
	for _, file := range files {
		if file["path"] == "renamed.md" && file["oldPath"] == "README.md" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("rename not captured: %v", files)
	}
	content, err := ReviewContent(ctx, common, base, head, "link", "")
	if err != nil {
		t.Fatal(err)
	}
	if content["after"] != "/etc/passwd" {
		t.Fatalf("symlink was followed: %v", content)
	}
}
