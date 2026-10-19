package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// CaptureReview records a complete working tree without changing the real index.
// The common git directory survives removal of a linked worktree. Tree refs
// protect both tracked and untracked content from git's object collection.
func CaptureReview(ctx context.Context, directory, refID string) (map[string]any, error) {
	if _, err := uuid.Parse(refID); err != nil {
		return nil, fmt.Errorf("invalid review reference")
	}
	root, err := git(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("this directory is not in a Git repository")
	}
	directory = strings.TrimSpace(root)
	common, err := git(ctx, directory, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common = strings.TrimSpace(common)
	temp, err := os.CreateTemp(common, "threavia-review-index-*")
	if err != nil {
		return nil, err
	}
	index := temp.Name()
	_ = temp.Close()
	defer os.Remove(index)
	// Start from the real index when it exists, including staged deletions and
	// conflict entries. An unborn repository has no index yet.
	indexPath, err := git(ctx, directory, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	data, readErr := os.ReadFile(strings.TrimSpace(indexPath))
	if readErr == nil {
		if err = os.WriteFile(index, data, 0600); err != nil {
			return nil, err
		}
		info, e := os.Stat(strings.TrimSpace(indexPath))
		if e != nil {
			return nil, e
		}
		if err = os.Chtimes(index, info.ModTime(), info.ModTime()); err != nil {
			return nil, err
		}
	} else if os.IsNotExist(readErr) {
		_ = os.Remove(index)
	} else {
		return nil, readErr
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err = gitWith(ctx, directory, env, "add", "--all"); err != nil {
		return nil, fmt.Errorf("could not capture review files: %w", err)
	}
	tree, err := gitWith(ctx, directory, env, "write-tree")
	if err != nil {
		return nil, err
	}
	tree = strings.TrimSpace(tree)
	if _, err = git(ctx, common, "update-ref", "refs/threavia/reviews/"+refID+"/"+tree, tree); err != nil {
		return nil, err
	}
	return map[string]any{"directory": directory, "gitDirectory": common, "name": filepath.Base(directory), "tree": tree}, nil
}

// ReviewFiles enumerates every changed file from the actual trees, independently
// of porcelain state, commits, summary limits or line-count heuristics.
func ReviewFiles(ctx context.Context, directory, base, head string) ([]map[string]any, error) {
	if !objectID.MatchString(base) || !objectID.MatchString(head) {
		return nil, fmt.Errorf("invalid review tree")
	}
	out, err := git(ctx, directory, "diff", "--no-ext-diff", "--no-textconv", "--name-status", "-z", "-M", base, head, "--")
	if err != nil {
		return nil, fmt.Errorf("review snapshots are unavailable on this backend")
	}
	files := []map[string]any{}
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); {
		code, path := parts[i], parts[i+1]
		i += 2
		if code == "" {
			break
		}
		state := "MODIFIED"
		file := map[string]any{"path": path}
		switch code[0] {
		case 'A':
			state = "ADDED"
		case 'D':
			state = "DELETED"
		case 'R', 'C':
			if i >= len(parts) {
				return nil, fmt.Errorf("invalid rename record")
			}
			file["oldPath"] = path
			file["path"] = parts[i]
			i++
			state = "RENAMED"
		}
		file["state"] = state
		files = append(files, file)
	}
	if len(files) > 20000 {
		return nil, fmt.Errorf("review exceeds 20000 changed files; split the work into smaller reviews")
	}
	return files, nil
}

func reviewText(ctx context.Context, directory, tree, path string) (string, bool, bool, error) {
	if !objectID.MatchString(tree) || path == "" || strings.ContainsRune(path, 0) || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
		return "", false, false, fmt.Errorf("invalid review file")
	}
	// A path absent on one side is a valid addition or deletion. A missing tree
	// is checked separately so it cannot masquerade as an empty file.
	if _, err := git(ctx, directory, "cat-file", "-e", tree+"^{tree}"); err != nil {
		return "", false, false, fmt.Errorf("review tree is unavailable")
	}
	size, err := git(ctx, directory, "cat-file", "-s", tree+":"+path)
	if err != nil {
		return "", false, false, nil
	}
	kind, err := git(ctx, directory, "cat-file", "-t", tree+":"+path)
	if err != nil {
		return "", false, false, err
	}
	if strings.TrimSpace(kind) != "blob" {
		return "", true, false, nil
	}
	var n int64
	if _, err = fmt.Sscan(size, &n); err != nil {
		return "", false, false, err
	}
	if n > maxDiff {
		return "", false, true, nil
	}
	value, err := git(ctx, directory, "show", "--no-ext-diff", "--no-textconv", tree+":"+path)
	if err != nil {
		return "", false, false, err
	}
	if strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
		return "", true, false, nil
	}
	return value, false, false, nil
}

// ReviewContent never executes filters or reads the current checkout. Symlinks
// are shown as their stored target text, never followed into the host filesystem.
func ReviewContent(ctx context.Context, directory, base, head, path, oldPath string) (map[string]any, error) {
	if oldPath == "" {
		oldPath = path
	}
	before, b1, t1, err := reviewText(ctx, directory, base, oldPath)
	if err != nil {
		return nil, err
	}
	after, b2, t2, err := reviewText(ctx, directory, head, path)
	if err != nil {
		return nil, err
	}
	if t1 || t2 {
		return map[string]any{"path": path, "before": before, "after": after, "diff": "", "binary": b1 || b2, "truncated": true}, nil
	}
	diff, err := DiffOf(ctx, directory, base, head, path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "before": before, "after": after, "diff": diff.Text, "binary": b1 || b2 || diff.Binary, "truncated": t1 || t2 || diff.Truncated}, nil
}
