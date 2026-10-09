package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/tools"
)

// cloneTimeout bounds an offered clone. A repository can be large, but a Job
// waiting on one must not wait forever.
const cloneTimeout = 10 * time.Minute

// ErrDirectoryUnresolved means the Job has a working directory Core knows about
// and this backend cannot locate.
var ErrDirectoryUnresolved = errors.New("the working directory could not be resolved on this backend")

// resolveWorkingDirectory finds where a KnownDirectory actually lives here, and
// records the binding so the next Job costs nothing (spec section 11).
//
// The order is the one the specification lays out: try deterministic resolution,
// then search only the configured discovery roots, then ask the user — offering
// a clone when the directory has a git remote and is simply not here yet. The
// agent is never started in a guessed directory.
func (a *Adapter) resolveWorkingDirectory(ctx context.Context, jobID string, pc *backendv1.ProjectContext) (string, error) {
	if path := pc.GetWorkingDirectoryPath(); path != "" {
		return path, nil
	}
	directoryID := pc.GetKnownDirectoryId()
	if directoryID == "" {
		// The Session has no working directory at all, so this backend decides
		// where unscoped work happens rather than inheriting its launch
		// directory.
		return a.cfg.Claude.DefaultWorkingDirectory, nil
	}

	name := pc.GetKnownDirectoryName()
	candidates := a.discover(name)

	if len(candidates) == 1 {
		a.logger.Info("working directory resolved by discovery",
			slog.String("directory", name), slog.String("path", candidates[0]))
		a.bindDirectory(ctx, jobID, directoryID, candidates[0])
		return candidates[0], nil
	}

	path, err := a.askForDirectory(ctx, jobID, name, pc.GetKnownDirectoryGitRemote(), candidates)
	if err != nil {
		return "", err
	}
	a.bindDirectory(ctx, jobID, directoryID, path)
	return path, nil
}

// discover looks for the directory under the configured roots, and nowhere
// else. The roots constrain discovery only; they are not a security boundary
// (spec sections 11 and 28).
func (a *Adapter) discover(name string) []string {
	if name == "" {
		return nil
	}

	var found []string
	seen := make(map[string]bool)
	for _, root := range a.cfg.Claude.DiscoveryRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() && !seen[candidate] {
			seen[candidate] = true
			found = append(found, candidate)
		}
	}
	return found
}

// askForDirectory asks the user where the directory is, and clones it when they
// choose that.
func (a *Adapter) askForDirectory(ctx context.Context, jobID, name, gitRemote string, candidates []string) (string, error) {
	const cloneChoice = "clone it here"

	choices := append([]string{}, candidates...)

	// What the discovery roots turned up decides what to say. Claiming the
	// directory is not here when several copies of it are would be a lie, and
	// claiming nothing when the roots found nothing leaves the user guessing why
	// they are being asked at all.
	prompt := fmt.Sprintf("Where is %q on this machine?", name)
	switch {
	case len(candidates) > 1:
		prompt += " Several copies are under the discovery roots, so pick one or answer with an absolute path."
	case len(a.cfg.Claude.DiscoveryRoots) == 0:
		prompt += " This backend has no discovery roots configured, so answer with an absolute path."
	default:
		prompt += fmt.Sprintf(" It is not under %s, so answer with an absolute path.",
			strings.Join(a.cfg.Claude.DiscoveryRoots, ", "))
	}

	if gitRemote != "" && len(a.cfg.Claude.DiscoveryRoots) > 0 {
		target := filepath.Join(a.cfg.Claude.DiscoveryRoots[0], name)
		prompt += fmt.Sprintf("\n\nOr answer %q to clone %s into %s.", cloneChoice, gitRemote, target)
		choices = append(choices, cloneChoice)
	}

	// Free text whatever the choices are: every one of them is a shortcut, and
	// the question is where the directory actually is.
	answer, err := a.AskUser(ctx, jobID, prompt, choices, true)
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(answer)

	if answer == cloneChoice {
		target := filepath.Join(a.cfg.Claude.DiscoveryRoots[0], name)
		if err := clone(ctx, gitRemote, target); err != nil {
			return "", err
		}
		a.logger.Info("working directory cloned",
			slog.String("directory", name), slog.String("path", target))
		return target, nil
	}

	if !filepath.IsAbs(answer) {
		return "", fmt.Errorf("%w: %q is not an absolute path", ErrDirectoryUnresolved, answer)
	}
	if info, err := os.Stat(answer); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %q is not a directory here", ErrDirectoryUnresolved, answer)
	}
	return answer, nil
}

// clone fetches a repository the user asked for. It is the one acquisition this
// backend performs on its own, and only after the user chose it.
func clone(ctx context.Context, remote, target string) error {
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("prepare %s: %w", filepath.Dir(target), err)
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--", remote, target)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: clone %s: %s", ErrDirectoryUnresolved, remote, strings.TrimSpace(string(out)))
	}
	return nil
}

// bindDirectory records where the directory lives here, so the next Job on this
// backend resolves it without asking anyone.
//
// A failure is logged rather than returned: the Job can run perfectly well in a
// directory Core has not yet been told about, and refusing to start it over a
// bookkeeping write would be the wrong trade.
func (a *Adapter) bindDirectory(ctx context.Context, jobID, directoryID, path string) {
	_, err := a.CallCoreTool(ctx, jobID, string(tools.NameKnownDirectoryBind), map[string]any{
		"knownDirectoryId": directoryID,
		"path":             path,
	}, "")
	if err != nil {
		a.logger.Error("cannot record the working directory binding",
			slog.String("knownDirectoryId", directoryID),
			slog.String("path", path),
			slog.String("error", err.Error()))
	}
}
