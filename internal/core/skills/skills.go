// Package skills acquires Core-managed Project Skills and packs them into
// immutable bundles (THREAVIA_SPEC_V1.md section 18).
//
// Acquisition is deliberately narrow: Core fetches, inspects and repacks. It
// never runs an installer, a build step or any other command the source
// contains. A Skill is data as far as Core is concerned, and the only thing that
// ever executes it is an agent, on a backend, under that backend's own
// permissions.
package skills

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// ErrInvalidSkill is returned when a source does not hold an Agent Skills-style
// directory.
var ErrInvalidSkill = errors.New("not a valid skill")

// Manifest is the canonical entry point of a Skill directory.
const Manifest = "SKILL.md"

// Limits bound what Core will fetch and unpack. They exist because the source of
// a Skill is a third party, and an unbounded download is a way to take a control
// plane down.
type Limits struct {
	// MaxBundleBytes bounds the packed bundle and each download.
	MaxBundleBytes int64
	// MaxFiles bounds how many entries a Skill directory may contain.
	MaxFiles int
	// FetchTimeout bounds one acquisition.
	FetchTimeout time.Duration
	// AllowLocalSources permits file:// git remotes. It is off by default: a
	// Skill source names a third party, and a local path would let one reach the
	// filesystem Core itself runs on. An air-gapped deployment with a local
	// mirror turns it on deliberately.
	AllowLocalSources bool
}

// DefaultLimits returns the limits Core uses unless configured otherwise.
func DefaultLimits() Limits {
	return Limits{
		MaxBundleBytes: 32 << 20, // 32 MiB
		MaxFiles:       2000,
		FetchTimeout:   2 * time.Minute,
	}
}

// Bundle is an acquired Skill, ready to be stored and distributed.
type Bundle struct {
	// Name and Description come from the SKILL.md front matter when it has one.
	Name        string
	Description string
	// InstalledRevision is the immutable identity of what was acquired: the
	// commit for a repository, the bundle checksum otherwise.
	InstalledRevision string
	// Content is the packed directory, a gzipped tar.
	Content []byte
	SHA256  string
}

// Acquirer fetches Skills from the sources of specification section 18.
type Acquirer struct {
	limits Limits
	http   *http.Client
}

// NewAcquirer builds an Acquirer.
func NewAcquirer(limits Limits) *Acquirer {
	if limits.MaxBundleBytes <= 0 {
		limits = DefaultLimits()
	}
	return &Acquirer{
		limits: limits,
		http:   &http.Client{Timeout: limits.FetchTimeout},
	}
}

// Acquire fetches a Skill and returns the immutable bundle to store.
//
// upload carries the bytes for an uploaded archive and is ignored otherwise.
func (a *Acquirer) Acquire(ctx context.Context, source domain.SkillSource, upload io.Reader) (Bundle, error) {
	ctx, cancel := context.WithTimeout(ctx, a.limits.FetchTimeout)
	defer cancel()

	root, err := os.MkdirTemp("", "threavia-skill-*")
	if err != nil {
		return Bundle{}, fmt.Errorf("prepare the acquisition directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()

	var revision string
	switch source.Type {
	case domain.SkillSourceGit:
		revision, err = a.fetchGit(ctx, source, root)
	case domain.SkillSourceArchive:
		err = a.fetchArchive(ctx, source, root)
	case domain.SkillSourceUpload:
		err = a.unpackArchive(upload, source.URL, root)
	default:
		err = fmt.Errorf("%w: unknown source type %q", ErrInvalidSkill, source.Type)
	}
	if err != nil {
		return Bundle{}, err
	}

	directory, err := skillDirectory(root, source.Path)
	if err != nil {
		return Bundle{}, err
	}

	bundle, err := a.pack(directory)
	if err != nil {
		return Bundle{}, err
	}
	if revision == "" {
		// Nothing upstream identifies this acquisition, so its own content does.
		revision = bundle.SHA256
	}
	bundle.InstalledRevision = revision
	return bundle, nil
}

// fetchGit clones a repository and reports the commit that was actually
// checked out, which is the immutable identity of the install.
func (a *Acquirer) fetchGit(ctx context.Context, source domain.SkillSource, root string) (string, error) {
	schemes := []string{"git", "https", "http", "ssh"}
	if a.limits.AllowLocalSources {
		schemes = append(schemes, "file")
	}
	if err := validateRemoteURL(source.URL, schemes...); err != nil {
		return "", err
	}

	args := []string{"clone", "--quiet", "--no-tags", "--depth", "1"}
	if source.Revision != "" {
		// A branch or tag can be fetched shallowly; a raw commit cannot, and the
		// fallback below handles that.
		args = append(args, "--branch", source.Revision)
	}
	args = append(args, "--", source.URL, root)

	if err := runGit(ctx, "", args...); err != nil {
		if source.Revision == "" {
			return "", fmt.Errorf("clone %s: %w", source.URL, err)
		}
		// The revision is probably a commit: a full clone can reach it.
		if err := runGit(ctx, "", "clone", "--quiet", "--no-tags", "--", source.URL, root); err != nil {
			return "", fmt.Errorf("clone %s: %w", source.URL, err)
		}
		if err := runGit(ctx, root, "checkout", "--quiet", "--detach", source.Revision); err != nil {
			return "", fmt.Errorf("checkout %s: %w", source.Revision, err)
		}
	}

	commit, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read the installed revision: %w", err)
	}
	// The repository metadata is provenance Core already recorded; shipping it to
	// every backend would multiply the bundle for nothing.
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		return "", fmt.Errorf("drop the repository metadata: %w", err)
	}
	return strings.TrimSpace(commit), nil
}

// fetchArchive downloads a .zip or .tar.gz and unpacks it.
func (a *Acquirer) fetchArchive(ctx context.Context, source domain.SkillSource, root string) error {
	if err := validateRemoteURL(source.URL, "https", "http"); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return fmt.Errorf("build the archive request: %w", err)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", source.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: unexpected status %s", source.URL, resp.Status)
	}
	return a.unpackArchive(resp.Body, source.URL, root)
}

// unpackArchive writes an archive into root, choosing the format from the name
// and falling back to sniffing the first bytes.
func (a *Acquirer) unpackArchive(body io.Reader, name, root string) error {
	if body == nil {
		return fmt.Errorf("%w: no archive content", ErrInvalidSkill)
	}

	// Buffered whole because a .zip is only readable with random access, and the
	// size is already bounded.
	content, err := io.ReadAll(io.LimitReader(body, a.limits.MaxBundleBytes+1))
	if err != nil {
		return fmt.Errorf("read the archive: %w", err)
	}
	if int64(len(content)) > a.limits.MaxBundleBytes {
		return fmt.Errorf("%w: archive exceeds %d bytes", ErrInvalidSkill, a.limits.MaxBundleBytes)
	}

	if isZip(name, content) {
		return a.unpackZip(content, root)
	}
	return a.unpackTarGz(content, root)
}

func isZip(name string, content []byte) bool {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return true
	}
	return len(content) >= 2 && content[0] == 'P' && content[1] == 'K'
}

func (a *Acquirer) unpackZip(content []byte, root string) error {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSkill, err)
	}

	var written int64
	for index, entry := range reader.File {
		if index >= a.limits.MaxFiles {
			return fmt.Errorf("%w: archive holds more than %d entries", ErrInvalidSkill, a.limits.MaxFiles)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		target, err := safeJoin(root, entry.Name)
		if err != nil {
			return err
		}
		body, err := entry.Open()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSkill, err)
		}
		n, err := a.writeFile(target, body)
		_ = body.Close()
		if err != nil {
			return err
		}
		if written += n; written > a.limits.MaxBundleBytes {
			return fmt.Errorf("%w: archive expands past %d bytes", ErrInvalidSkill, a.limits.MaxBundleBytes)
		}
	}
	return nil
}

func (a *Acquirer) unpackTarGz(content []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSkill, err)
	}
	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	var written int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSkill, err)
		}
		if count >= a.limits.MaxFiles {
			return fmt.Errorf("%w: archive holds more than %d entries", ErrInvalidSkill, a.limits.MaxFiles)
		}
		// Only regular files survive: a symlink or a device node in a third-party
		// archive is a way out of the directory it was unpacked into.
		if header.Typeflag != tar.TypeReg {
			continue
		}
		target, err := safeJoin(root, header.Name)
		if err != nil {
			return err
		}
		n, err := a.writeFile(target, reader)
		if err != nil {
			return err
		}
		if written += n; written > a.limits.MaxBundleBytes {
			return fmt.Errorf("%w: archive expands past %d bytes", ErrInvalidSkill, a.limits.MaxBundleBytes)
		}
	}
}

// writeFile stores one bounded entry.
func (a *Acquirer) writeFile(target string, body io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("prepare %s: %w", filepath.Dir(target), err)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("write %s: %w", target, err)
	}
	defer func() { _ = file.Close() }()

	written, err := io.Copy(file, io.LimitReader(body, a.limits.MaxBundleBytes+1))
	if err != nil {
		return written, fmt.Errorf("write %s: %w", target, err)
	}
	return written, nil
}

// safeJoin resolves an archive entry inside root, refusing anything that would
// land outside it.
func safeJoin(root, name string) (string, error) {
	cleaned := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))

	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: entry %q escapes the archive root", ErrInvalidSkill, name)
	}
	return target, nil
}

// skillDirectory locates the Agent Skills directory inside what was acquired.
//
// An archive commonly wraps everything in a single top-level directory, so one
// level of that is unwrapped rather than demanded of the user.
func skillDirectory(root, requested string) (string, error) {
	if requested != "" {
		directory, err := safeJoin(root, requested)
		if err != nil {
			return "", err
		}
		if err := requireManifest(directory); err != nil {
			return "", err
		}
		return directory, nil
	}

	if err := requireManifest(root); err == nil {
		return root, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("inspect the acquired content: %w", err)
	}
	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	if len(directories) == 1 {
		nested := filepath.Join(root, directories[0])
		if err := requireManifest(nested); err == nil {
			return nested, nil
		}
	}
	return "", fmt.Errorf("%w: no %s found", ErrInvalidSkill, Manifest)
}

func requireManifest(directory string) error {
	info, err := os.Stat(filepath.Join(directory, Manifest))
	if err != nil || info.IsDir() {
		return fmt.Errorf("%w: no %s in %s", ErrInvalidSkill, Manifest, filepath.Base(directory))
	}
	return nil
}

// pack writes the Skill directory as a deterministic gzipped tar.
//
// Deterministic on purpose: the same content must yield the same checksum, so a
// backend can tell a cached copy from a stale one, and so re-acquiring an
// unchanged Skill is visibly a no-op.
func (a *Acquirer) pack(directory string) (Bundle, error) {
	var paths []string
	err := filepath.WalkDir(directory, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			// Directories are implied by their entries; anything else (a symlink,
			// a socket) has no place in a distributable bundle.
			return nil
		}
		relative, err := filepath.Rel(directory, current)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("read the skill directory: %w", err)
	}
	if len(paths) > a.limits.MaxFiles {
		return Bundle{}, fmt.Errorf("%w: skill holds more than %d files", ErrInvalidSkill, a.limits.MaxFiles)
	}
	sort.Strings(paths)

	var packed bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	writer := tar.NewWriter(gz)

	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
		if err != nil {
			return Bundle{}, fmt.Errorf("read %s: %w", relative, err)
		}
		header := &tar.Header{
			Name:     relative,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
			// A fixed timestamp: the bundle identity is its content, not when it
			// happened to be packed.
			ModTime: time.Unix(0, 0).UTC(),
			Format:  tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			return Bundle{}, fmt.Errorf("pack %s: %w", relative, err)
		}
		if _, err := writer.Write(content); err != nil {
			return Bundle{}, fmt.Errorf("pack %s: %w", relative, err)
		}
	}
	if err := writer.Close(); err != nil {
		return Bundle{}, fmt.Errorf("close the bundle: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Bundle{}, fmt.Errorf("compress the bundle: %w", err)
	}
	if int64(packed.Len()) > a.limits.MaxBundleBytes {
		return Bundle{}, fmt.Errorf("%w: bundle exceeds %d bytes", ErrInvalidSkill, a.limits.MaxBundleBytes)
	}

	manifest, err := os.ReadFile(filepath.Join(directory, Manifest))
	if err != nil {
		return Bundle{}, fmt.Errorf("read %s: %w", Manifest, err)
	}
	name, description := describe(manifest, filepath.Base(directory))

	digest := sha256.Sum256(packed.Bytes())
	return Bundle{
		Name:        name,
		Description: description,
		Content:     packed.Bytes(),
		SHA256:      hex.EncodeToString(digest[:]),
	}, nil
}

// describe reads the name and description out of the SKILL.md front matter,
// falling back to the directory name. Core only reads these two fields: the rest
// of the manifest is for the agent, and Core does not interpret it.
func describe(manifest []byte, fallbackName string) (name, description string) {
	name = fallbackName
	lines := strings.Split(string(manifest), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return name, ""
	}

	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			if value != "" {
				name = value
			}
		case "description":
			description = value
		}
	}
	return name, description
}

// validateRemoteURL refuses anything but the schemes a source may use. It keeps
// a Skill source from naming a local file or an unexpected protocol.
func validateRemoteURL(raw string, schemes ...string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid url %q", ErrInvalidSkill, raw)
	}
	// A scp-style git remote (git@host:path) has no scheme and is still valid.
	if parsed.Scheme == "" && strings.Contains(raw, "@") && strings.Contains(raw, ":") {
		return nil
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			// A file:// url names a path, not a host.
			if scheme == "file" {
				return nil
			}
			if parsed.Host == "" {
				return fmt.Errorf("%w: url %q has no host", ErrInvalidSkill, raw)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: unsupported url scheme %q", ErrInvalidSkill, parsed.Scheme)
}

// runGit runs one git command. Nothing from the acquired content is ever
// executed: these are git's own operations, with the source as an argument.
func runGit(ctx context.Context, directory string, args ...string) error {
	_, err := gitOutput(ctx, directory, args...)
	return err
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(),
		// No credential prompt and no interactive host-key question: an
		// acquisition must fail rather than hang a request.
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_SSH_COMMAND=ssh -oBatchMode=yes",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("%w: %s", ErrInvalidSkill, message)
	}
	return stdout.String(), nil
}
