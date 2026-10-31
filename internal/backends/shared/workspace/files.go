package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Bounds on naming files: a repository can hold hundreds of thousands, and a
// person typing @ wants the few that match, quickly.
const (
	// DefaultFileLimit is how many paths answer a query that asks for none.
	DefaultFileLimit = 50
	// MaxFileLimit is the most paths one answer carries.
	MaxFileLimit = 200
	// maxListedFiles is how many names are read before giving up on the rest.
	maxListedFiles = 200_000
)

// skippedDirectories are not walked outside git, where nothing says what is
// generated: they hold the most files and the fewest a person would name.
var skippedDirectories = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, "target": true, "dist": true, "build": true, ".direnv": true,
}

// FilesOf lists the files and directories of a working directory whose path
// matches a query, best match first, and says whether more matched than were
// returned.
//
// In a git repository the names are those git knows of, tracked or not, minus
// what it ignores; elsewhere the directory is walked, without the directories
// that only hold what a build produced. Only names are read, never content.
func FilesOf(ctx context.Context, directory, query string, limit int) ([]string, bool, error) {
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return nil, false, errors.New("the working directory does not exist on this machine")
	}
	if limit <= 0 {
		limit = DefaultFileLimit
	}
	limit = min(limit, MaxFileLimit)

	files, err := gitFiles(ctx, directory)
	if err != nil {
		files, err = walkFiles(ctx, directory)
		if err != nil {
			return nil, false, err
		}
	}
	return matchFiles(withDirectories(files), query, limit)
}

// gitFiles lists what git knows of, tracked or not, minus what it ignores.
func gitFiles(ctx context.Context, directory string) ([]string, error) {
	if _, err := git(ctx, directory, "rev-parse", "--git-dir"); err != nil {
		return nil, err
	}
	out, err := git(ctx, directory, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	names := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	if len(names) == 1 && names[0] == "" {
		return nil, nil
	}
	return names[:min(len(names), maxListedFiles)], nil
}

// walkFiles lists the files of a directory that is not a git repository.
func walkFiles(ctx context.Context, directory string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A directory that cannot be read is skipped, not fatal: the rest
			// is still worth naming.
			if entry != nil && entry.IsDir() && name != directory {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if name != directory && skippedDirectories[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(relative))
		if len(files) >= maxListedFiles {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, errors.New("the working directory could not be read")
	}
	return files, nil
}

// withDirectories adds the directories the files are in, each once, with a
// trailing slash: naming a whole directory is as common as naming a file.
func withDirectories(files []string) []string {
	seen := map[string]bool{}
	all := make([]string, 0, len(files)+len(files)/4)
	for _, file := range files {
		if file == "" {
			continue
		}
		all = append(all, file)
		for dir := path.Dir(file); dir != "." && dir != "/" && !seen[dir]; dir = path.Dir(dir) {
			seen[dir] = true
			all = append(all, dir+"/")
		}
	}
	return all
}

// matchFiles keeps the paths that match a query and sorts them best first.
func matchFiles(paths []string, query string, limit int) ([]string, bool, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		path  string
		score int
	}
	matches := make([]scored, 0, min(len(paths), 1024))
	for _, candidate := range paths {
		if score, ok := fileScore(candidate, query); ok {
			matches = append(matches, scored{candidate, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	truncated := len(matches) > limit
	matches = matches[:min(len(matches), limit)]
	result := make([]string, len(matches))
	for i, match := range matches {
		result[i] = match.path
	}
	return result, truncated, nil
}

// fileScore says how well a path matches what was typed, lower being better:
// the name itself before the directories it is in, a start before a middle,
// and letters in order last.
func fileScore(candidate, query string) (int, bool) {
	if query == "" {
		// Shallow paths first: the top of the tree is what a person scans.
		return strings.Count(strings.TrimSuffix(candidate, "/"), "/"), true
	}
	lower := strings.ToLower(candidate)
	base := path.Base(strings.TrimSuffix(lower, "/"))
	switch {
	case base == query || base == strings.TrimSuffix(query, "/"):
		return 0, true
	case strings.HasPrefix(base, query):
		return 1, true
	case strings.Contains(base, query):
		return 2, true
	case strings.HasPrefix(lower, query):
		return 3, true
	case strings.Contains(lower, query):
		return 4, true
	case inOrder(lower, query):
		return 5, true
	}
	return 0, false
}

// inOrder reports whether every character of query appears in s, in order.
func inOrder(s, query string) bool {
	wanted := []rune(query)
	i := 0
	for _, r := range s {
		if i < len(wanted) && r == wanted[i] {
			i++
		}
	}
	return i == len(wanted)
}
