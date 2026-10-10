// Package skills holds the backend side of THREAVIA_SPEC_V1.md section 18: the
// local cache of Core-managed Project Skills, the backend's own private Skills,
// and the mapping of both onto what Claude Code reads.
//
// The effective Skills of a Run are the Core-managed Project Skills plus the
// backend-local ones. Core never learns the content of a local Skill: it may
// only exist behind a corporate network, and Core holding it would defeat the
// point.
package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Manifest is the entry point of an Agent Skills directory.
const Manifest = "SKILL.md"

// maxEntryBytes bounds one file inside a bundle.
const maxEntryBytes = 32 << 20

// Cache stores unpacked Skill bundles under an immutable revision, so a Job
// that needs a Skill it already has fetches nothing.
type Cache struct {
	root string

	mu sync.Mutex
}

// NewCache builds the cache under root.
func NewCache(root string) *Cache { return &Cache{root: root} }

// Path is where a given revision of a Skill lives. The revision is part of the
// path, so a new version never overwrites one a running Job is reading.
func (c *Cache) Path(skillID, revision string) string {
	return filepath.Join(c.root, sanitise(skillID), sanitise(revision))
}

// Has reports whether a revision is already unpacked and usable.
func (c *Cache) Has(skillID, revision string) bool {
	info, err := os.Stat(filepath.Join(c.Path(skillID, revision), Manifest))
	return err == nil && !info.IsDir()
}

// Store unpacks a bundle into the cache.
//
// It writes to a temporary directory and renames, so a crash halfway through
// never leaves a half-unpacked Skill that Has would then call usable.
func (c *Cache) Store(skillID, revision string, bundle []byte) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	final := c.Path(skillID, revision)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", fmt.Errorf("prepare the skill cache: %w", err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(final), ".staging-*")
	if err != nil {
		return "", fmt.Errorf("prepare the skill cache: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if err := unpack(bundle, staging); err != nil {
		return "", err
	}
	if info, err := os.Stat(filepath.Join(staging, Manifest)); err != nil || info.IsDir() {
		return "", fmt.Errorf("the bundle holds no %s", Manifest)
	}

	// An existing revision is the same immutable content by definition, so
	// replacing it is safe and keeps a partially written one from surviving.
	if err := os.RemoveAll(final); err != nil {
		return "", fmt.Errorf("replace the cached skill: %w", err)
	}
	if err := os.Rename(staging, final); err != nil {
		return "", fmt.Errorf("install the cached skill: %w", err)
	}
	return final, nil
}

// unpack writes a gzipped tar into a directory, refusing anything that is not a
// plain file or that would land outside it.
func unpack(bundle []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return fmt.Errorf("read the skill bundle: %w", err)
	}
	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the skill bundle: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}

		target, err := safeJoin(root, header.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("prepare %s: %w", filepath.Dir(target), err)
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		_, err = io.Copy(file, io.LimitReader(reader, maxEntryBytes))
		closeErr := file.Close()
		if err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		if closeErr != nil {
			return fmt.Errorf("write %s: %w", target, closeErr)
		}
	}
}

// safeJoin resolves a bundle entry inside root, refusing anything that escapes.
func safeJoin(root, name string) (string, error) {
	cleaned := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("bundle entry %q escapes its directory", name)
	}
	return target, nil
}

// sanitise keeps an identifier usable as a single path segment.
func sanitise(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "unnamed"
	}
	return b.String()
}

// pluginManifest is the Claude Code plugin descriptor. Skills reach the provider
// as a session-scoped plugin: it is the one mechanism that adds Skills for a
// single run without writing anything into the user's repository or into their
// Claude Code configuration.
type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

// Assemble builds the per-Job plugin directory exposing every effective Skill,
// and returns its path. An empty set yields an empty path: there is nothing to
// hand the provider.
func Assemble(directory string, sets ...[]Entry) (string, error) {
	var entries []Entry
	for _, set := range sets {
		entries = append(entries, set...)
	}
	if len(entries) == 0 {
		return "", nil
	}

	if err := os.RemoveAll(directory); err != nil {
		return "", fmt.Errorf("prepare the skill directory: %w", err)
	}
	manifestDir := filepath.Join(directory, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return "", fmt.Errorf("prepare the skill directory: %w", err)
	}

	manifest, err := json.Marshal(pluginManifest{
		Name:        "threavia-skills",
		Description: "Project and local skills provided by Threavia for this run.",
		Version:     "1.0.0",
	})
	if err != nil {
		return "", fmt.Errorf("write the plugin manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), manifest, 0o644); err != nil {
		return "", fmt.Errorf("write the plugin manifest: %w", err)
	}

	skillsDir := filepath.Join(directory, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return "", fmt.Errorf("prepare the skill directory: %w", err)
	}

	seen := make(map[string]bool, len(entries))
	var linked int
	for _, entry := range entries {
		name := sanitise(entry.Name)
		if name == "" || seen[name] {
			// A Project Skill and a local one may share a name. The first wins,
			// and the sets are ordered so that is the Project one.
			continue
		}
		seen[name] = true

		// A symlink rather than a copy: a bundle is immutable and already on
		// disk, and copying it per Job would multiply it for nothing.
		if err := os.Symlink(entry.Path, filepath.Join(skillsDir, name)); err != nil {
			return "", fmt.Errorf("expose the skill %q: %w", entry.Name, err)
		}
		linked++
	}
	if linked == 0 {
		return "", nil
	}
	return directory, nil
}

// Entry is one effective Skill, ready to be exposed to the provider.
type Entry struct {
	Name string
	Path string
}
