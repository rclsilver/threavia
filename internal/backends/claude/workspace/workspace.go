// Package workspace turns what an agent did to the filesystem into the
// structured change summary of THREAVIA_SPEC_V1.md section 22.
//
// The filesystem stays backend-owned: Core receives the list of files touched
// and the line counts, never the diff. A detailed diff is fetched from the
// backend on demand, so nothing here persists one.
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// commandTimeout bounds one git invocation. A repository can be large, and a
// change summary must never hold a Job open.
const commandTimeout = 30 * time.Second

// maxFiles bounds the reported list. A refactor can touch thousands of files;
// the counts stay exact while the list stays a timeline entry.
const maxFiles = 200

// State is the state of one path, in the vocabulary of the protocol.
type State string

const (
	StateAdded    State = "ADDED"
	StateModified State = "MODIFIED"
	StateDeleted  State = "DELETED"
	StateRenamed  State = "RENAMED"
)

// File is one changed path.
type File struct {
	Path  string
	State State
}

// Summary is what a Job did to a working directory.
type Summary struct {
	Files     []File
	Additions int32
	Deletions int32
	// Truncated reports that more paths changed than the list carries.
	Truncated bool
	// Directory and the two trees are what a diff is asked for with later:
	// the working directory, and its git trees before and after the Job.
	// Empty trees mean they could not be captured, and no diff can be shown.
	Directory string
	BaseTree  string
	HeadTree  string
}

// Empty reports that nothing changed, in which case there is nothing to say.
func (s Summary) Empty() bool { return len(s.Files) == 0 && s.Additions == 0 && s.Deletions == 0 }

// Snapshot is the observed state of a working directory at one instant. Two of
// them bracket a Job, so what the agent changed is told apart from what was
// already dirty when it started.
type Snapshot struct {
	// tracked is a git-detectable repository. Without one there is nothing to
	// compare and no summary is produced: guessing from modification times would
	// report a build directory as agent work.
	tracked bool
	states  map[string]State
	// stats are the per-path line counts, so the summary subtracts what was
	// already there.
	stats map[string]lineCount
	// tree is the git tree of the whole working directory, uncommitted and
	// untracked files included, which is what a diff is computed against.
	tree string
}

type lineCount struct {
	additions int32
	deletions int32
}

// Observe captures the state of a working directory.
//
// A directory that is not a git repository yields an untracked Snapshot, and
// comparing two of those reports nothing: Threavia says what it can see rather
// than inventing a change list.
func Observe(ctx context.Context, directory string) Snapshot {
	snapshot := Snapshot{
		states: make(map[string]State),
		stats:  make(map[string]lineCount),
	}
	if directory == "" {
		return snapshot
	}
	if out, err := git(ctx, directory, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return snapshot
	}
	snapshot.tracked = true

	if out, err := git(ctx, directory, "status", "--porcelain=v1", "--untracked-files=all", "-z"); err == nil {
		for path, state := range parseStatus(out) {
			snapshot.states[path] = state
		}
	}
	// Both the staged and the unstaged counts, so a Job that commits its work is
	// not reported as having changed nothing.
	if out, err := git(ctx, directory, "diff", "--numstat", "HEAD"); err == nil {
		for path, count := range parseNumstat(out) {
			snapshot.stats[path] = count
		}
	}
	snapshot.tree = treeOf(ctx, directory)
	return snapshot
}

// treeOf records the working directory as a git tree, without touching what
// the person sees: their index, their branches and their stash stay as they
// were.
//
// It works on a copy of the index, so only the files that changed since it
// was written are hashed again, which keeps it cheap on a large repository.
// The one trace it leaves is unreferenced objects in the repository's object
// store, which git's own maintenance collects after a couple of weeks — and
// that is how long a diff stays available.
func treeOf(ctx context.Context, directory string) string {
	index, err := git(ctx, directory, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return ""
	}
	scratch, err := os.CreateTemp("", "threavia-index-*")
	if err != nil {
		return ""
	}
	defer func() { _ = os.Remove(scratch.Name()) }()
	if current, err := os.ReadFile(strings.TrimSpace(index)); err == nil {
		_, _ = scratch.Write(current)
	}
	_ = scratch.Close()

	env := "GIT_INDEX_FILE=" + scratch.Name()
	if _, err := gitWith(ctx, directory, []string{env}, "add", "--all"); err != nil {
		return ""
	}
	out, err := gitWith(ctx, directory, []string{env}, "write-tree")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// maxDiff bounds one file's diff: past this it is a regenerated file or a
// dump, and a reader is better served by a note than by the bytes.
const maxDiff = 512 << 10

// objectID is what a tree id looks like, SHA-1 or SHA-256.
var objectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// Diff is one file's change between two trees.
type Diff struct {
	Text      string
	Binary    bool
	Truncated bool
}

// DiffOf computes the diff of one path between the trees a Summary reported.
//
// The path is the one the summary listed, relative to the top of the
// repository, and it is matched literally: a name with a glob character in it
// is a name, not a pattern.
func DiffOf(ctx context.Context, directory, base, head, path string) (Diff, error) {
	if !objectID.MatchString(base) || !objectID.MatchString(head) {
		return Diff{}, fmt.Errorf("no record of this change to compare")
	}
	if path == "" {
		return Diff{}, fmt.Errorf("no file named")
	}
	out, err := git(ctx, directory, "diff", "--no-color", "--no-ext-diff", "--no-textconv",
		base, head, "--", ":(top,literal)"+path)
	if err != nil {
		// The objects are unreferenced on purpose, so git collects them in
		// time; and the directory may have been removed since.
		return Diff{}, fmt.Errorf("this change is no longer available on the backend")
	}
	if strings.HasPrefix(out, "Binary files ") || strings.Contains(out, "\nBinary files ") {
		return Diff{Binary: true}, nil
	}
	if len(out) > maxDiff {
		return Diff{Text: out[:maxDiff], Truncated: true}, nil
	}
	return Diff{Text: out}, nil
}

// Since returns what changed between the snapshot taken before a Job and the
// state of the directory now.
func Since(ctx context.Context, directory string, before Snapshot) Summary {
	if !before.tracked {
		return Summary{}
	}

	after := Observe(ctx, directory)
	if !after.tracked {
		return Summary{}
	}

	summary := Summary{Directory: directory, BaseTree: before.tree, HeadTree: after.tree}
	for path, state := range after.states {
		if previous, existed := before.states[path]; existed && previous == state {
			// Already in this state when the Job started: not its doing.
			continue
		}
		summary.Files = append(summary.Files, File{Path: path, State: state})
	}

	// A path that was dirty before and is clean now was reverted, which is a
	// change the timeline should carry too.
	for path := range before.states {
		if _, still := after.states[path]; !still {
			summary.Files = append(summary.Files, File{Path: path, State: StateModified})
		}
	}

	for path, count := range after.stats {
		previous := before.stats[path]
		summary.Additions += count.additions - previous.additions
		summary.Deletions += count.deletions - previous.deletions
	}
	// Line counts are a delta of two states, so a reverted edit can make them
	// negative. Reporting a negative addition count would be nonsense.
	summary.Additions = max(summary.Additions, 0)
	summary.Deletions = max(summary.Deletions, 0)

	sort.Slice(summary.Files, func(i, j int) bool { return summary.Files[i].Path < summary.Files[j].Path })
	if len(summary.Files) > maxFiles {
		summary.Files = summary.Files[:maxFiles]
		summary.Truncated = true
	}
	return summary
}

// parseStatus reads the NUL-separated porcelain v1 output.
//
// Each record is "XY path", and a rename adds a second record holding the
// original path, which is consumed with it.
func parseStatus(out string) map[string]State {
	states := make(map[string]State)
	records := strings.Split(out, "\x00")

	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 {
			continue
		}
		code := record[:2]
		path := record[3:]

		state := stateOf(code)
		if state == StateRenamed {
			// The original path follows in its own record.
			i++
		}
		if path != "" {
			states[path] = state
		}
	}
	return states
}

// stateOf maps a porcelain status code onto a reported state. The index and the
// work tree are merged on purpose: the timeline says what the file became, not
// where git is holding it.
func stateOf(code string) State {
	switch {
	case strings.ContainsRune(code, 'R'):
		return StateRenamed
	case strings.ContainsRune(code, 'D'):
		return StateDeleted
	case code == "??", strings.ContainsRune(code, 'A'):
		return StateAdded
	default:
		return StateModified
	}
}

// parseNumstat reads "additions<TAB>deletions<TAB>path" lines. A binary file
// shows "-" for both, and contributes no line counts.
func parseNumstat(out string) map[string]lineCount {
	counts := make(map[string]lineCount)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(fields) != 3 {
			continue
		}
		additions, errAdd := strconv.Atoi(fields[0])
		deletions, errDel := strconv.Atoi(fields[1])
		if errAdd != nil || errDel != nil {
			continue
		}
		counts[fields[2]] = lineCount{additions: int32(additions), deletions: int32(deletions)}
	}
	return counts
}

// git runs one git command in a directory.
func git(ctx context.Context, directory string, args ...string) (string, error) {
	return gitWith(ctx, directory, nil, args...)
}

// gitWith runs one git command with extra environment.
func gitWith(ctx context.Context, directory string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	// A repository the backend account does not own would otherwise make git
	// refuse, and a refusal here must stay a missing summary, never a failed Job.
	cmd.Env = append(append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0"), env...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
