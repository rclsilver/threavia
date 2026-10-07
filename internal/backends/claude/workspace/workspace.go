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
	"os/exec"
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
	return snapshot
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

	var summary Summary
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

// git runs one read-only git command in a directory.
func git(ctx context.Context, directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	// A repository the backend account does not own would otherwise make git
	// refuse, and a refusal here must stay a missing summary, never a failed Job.
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
