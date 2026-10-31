package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFilesOfNamesWhatGitKnowsBestMatchFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	repo := newRepository(t)
	write(t, repo, ".gitignore", "secret.env\nout/\n")
	write(t, repo, "web/ui/src/composer.tsx", "x\n")
	write(t, repo, "web/ui/src/routes/session.tsx", "x\n")
	write(t, repo, "internal/compose.go", "x\n")
	write(t, repo, "secret.env", "x\n")
	write(t, repo, "out/bundle.js", "x\n")
	gitIn(t, repo, "add", ".gitignore", "web", "internal/compose.go")
	gitIn(t, repo, "commit", "--quiet", "-m", "files")
	// Untracked but not ignored: a file the agent just wrote is nameable.
	write(t, repo, "notes/compose-plan.md", "x\n")

	got, truncated, err := FilesOf(ctx, repo, "compos", 0)
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	// Equally good matches: the shorter path first.
	want := []string{"internal/compose.go", "notes/compose-plan.md", "web/ui/src/composer.tsx"}
	if !slices.Equal(got, want) || truncated {
		t.Fatalf("matching compos: got %v (truncated %v), want %v", got, truncated, want)
	}

	all, _, err := FilesOf(ctx, repo, "", MaxFileLimit)
	if err != nil {
		t.Fatalf("all files: %v", err)
	}
	for _, ignored := range []string{"secret.env", "out/bundle.js", "out/"} {
		if slices.Contains(all, ignored) {
			t.Fatalf("%s is ignored by git and must not be offered: %v", ignored, all)
		}
	}
	for _, directory := range []string{"web/", "web/ui/src/routes/"} {
		if !slices.Contains(all, directory) {
			t.Fatalf("the directory %s must be nameable: %v", directory, all)
		}
	}
	// Shallow first when nothing is typed.
	if strings.Contains(strings.TrimSuffix(all[0], "/"), "/") {
		t.Fatalf("an empty query starts at the top of the tree: got %v", all)
	}

	// Letters in order match too, after everything that matched better.
	got, _, _ = FilesOf(ctx, repo, "wrss", 0)
	if !slices.Contains(got, "web/ui/src/routes/session.tsx") {
		t.Fatalf("letters in order: got %v", got)
	}

	got, truncated, _ = FilesOf(ctx, repo, "", 2)
	if len(got) != 2 || !truncated {
		t.Fatalf("a limit is a limit and says so: got %v (truncated %v)", got, truncated)
	}
}

func TestFilesOfWalksADirectoryOutsideGit(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	write(t, directory, "src/main.go", "x\n")
	write(t, directory, "node_modules/left-pad/index.js", "x\n")

	got, _, err := FilesOf(context.Background(), directory, "", 0)
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	if !slices.Contains(got, "src/main.go") || !slices.Contains(got, "src/") {
		t.Fatalf("the files of a plain directory: got %v", got)
	}
	if slices.Contains(got, "node_modules/left-pad/index.js") {
		t.Fatalf("node_modules is not walked: got %v", got)
	}

	if _, _, err := FilesOf(context.Background(), filepath.Join(directory, "missing"), "", 0); err == nil {
		t.Fatal("a directory that does not exist is an error")
	}
	if err := os.WriteFile(filepath.Join(directory, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FilesOf(context.Background(), filepath.Join(directory, "file"), "", 0); err == nil {
		t.Fatal("a file is not a working directory")
	}
}
