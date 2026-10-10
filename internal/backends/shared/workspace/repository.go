package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// fetchTimeout bounds a fetch someone asked for. A remote that does not answer
// must leave the person with the state as last known, not with a spinner.
const fetchTimeout = 30 * time.Second

// Repository is where a working directory stands in git: on which branch,
// how far from its upstream, and what is not committed yet.
type Repository struct {
	Directory string
	// Tracked is false for a directory that is not in a git repository, in
	// which case nothing else is set.
	Tracked bool
	// Branch is empty when HEAD is detached; Head is the commit either way,
	// abbreviated, and empty in a repository with no commit yet.
	Branch string
	Head   string
	// Upstream is the branch this one tracks, such as origin/main, and empty
	// when it tracks none. Ahead and Behind count against it as last fetched.
	Upstream string
	Ahead    int32
	Behind   int32
	// UpstreamGone is set when the branch tracks one that no longer exists.
	UpstreamGone bool
	Staged       int32
	Unstaged     int32
	Untracked    int32
	Conflicted   int32
	// FetchedAt is when the repository last heard from a remote, zero when it
	// never did. It is what "up to date" means without a fetch.
	FetchedAt time.Time
	// FetchError says why a fetch asked for did not happen. The rest is still
	// the state as last known.
	FetchError string
}

// Clean reports nothing to commit: no change staged or not, nothing untracked.
func (r Repository) Clean() bool {
	return r.Staged == 0 && r.Unstaged == 0 && r.Untracked == 0 && r.Conflicted == 0
}

// StatusOf reads where a working directory stands, fetching from its remote
// first when asked to.
//
// Reading never writes: no lock is taken and the index is not refreshed. The
// fetch, when asked for, only updates the remote-tracking branches — never the
// files, the branch checked out or the index — and never prompts for a
// credential: one that is not at hand makes it fail, and that is said.
func StatusOf(ctx context.Context, directory string, fetch bool) (Repository, error) {
	repo := Repository{Directory: directory}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return repo, errors.New("the working directory does not exist on this machine")
	}
	if _, err := git(ctx, directory, "rev-parse", "--git-dir"); err != nil {
		return repo, nil
	}
	repo.Tracked = true

	if fetch {
		repo.FetchError = fetchRemote(ctx, directory)
	}

	out, err := git(ctx, directory, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return repo, errors.New("git could not read the status of this repository")
	}
	parseRepository(out, &repo)
	repo.FetchedAt = fetchedAt(ctx, directory)
	return repo, nil
}

// fetchRemote fetches what the checked-out branch tracks, and says why not
// when it could not.
func fetchRemote(ctx context.Context, directory string) string {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	env := []string{"GIT_TERMINAL_PROMPT=0"}
	// An ssh remote whose key wants a passphrase would otherwise wait on a
	// terminal nobody watches.
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	cmd := gitCommand(ctx, directory, env, "fetch", "--quiet", "--no-tags")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	if ctx.Err() != nil {
		return "the remote did not answer in time"
	}
	if message := strings.TrimSpace(string(out)); message != "" {
		// The last line is the one that says what went wrong.
		lines := strings.Split(message, "\n")
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return err.Error()
}

// fetchedAt is when the repository last fetched, read from FETCH_HEAD.
func fetchedAt(ctx context.Context, directory string) time.Time {
	out, err := git(ctx, directory, "rev-parse", "--git-path", "FETCH_HEAD")
	if err != nil {
		return time.Time{}
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// parseRepository reads `git status --porcelain=v2 --branch`.
func parseRepository(out string, repo *Repository) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			if oid := strings.TrimPrefix(line, "# branch.oid "); oid != "(initial)" && len(oid) >= 7 {
				repo.Head = oid[:7]
			}
		case strings.HasPrefix(line, "# branch.head "):
			if head := strings.TrimPrefix(line, "# branch.head "); head != "(detached)" {
				repo.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			repo.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
			// Git prints no branch.ab for an upstream that is gone; it is set
			// back below when it does.
			repo.UpstreamGone = true
		case strings.HasPrefix(line, "# branch.ab "):
			repo.UpstreamGone = false
			for _, field := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				count, err := strconv.ParseInt(field[1:], 10, 32)
				if err != nil {
					continue
				}
				if field[0] == '+' {
					repo.Ahead = int32(count)
				} else {
					repo.Behind = int32(count)
				}
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			if len(line) < 4 {
				continue
			}
			if line[2] != '.' {
				repo.Staged++
			}
			if line[3] != '.' {
				repo.Unstaged++
			}
		case strings.HasPrefix(line, "u "):
			repo.Conflicted++
		case strings.HasPrefix(line, "? "):
			repo.Untracked++
		}
	}
}
