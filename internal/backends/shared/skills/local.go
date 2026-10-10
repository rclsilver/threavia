package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Local is a Skill that exists only on this backend (spec section 18).
//
// Core is told its name, description and availability, and never its content: a
// local Skill may live behind a corporate network, and the point of the split is
// that Core does not need to reach it.
type Local struct {
	Name        string
	Description string
	Path        string
	Available   bool
}

// Discover scans the configured directories for Agent Skills.
//
// A directory holding a SKILL.md is a Skill; a directory of such directories is
// a collection of them. Anything else is ignored rather than reported as broken:
// these are the user's own directories, not something Threavia manages.
func Discover(roots []string) []Local {
	var found []Local
	seen := make(map[string]bool)

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		for _, candidate := range candidates(root) {
			manifest := filepath.Join(candidate, Manifest)
			content, err := os.ReadFile(manifest)
			if err != nil {
				continue
			}
			name, description := describe(content, filepath.Base(candidate))
			if seen[name] {
				continue
			}
			seen[name] = true
			found = append(found, Local{
				Name:        name,
				Description: description,
				Path:        candidate,
				Available:   true,
			})
		}
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}

// candidates returns the directories to inspect for a configured root: the root
// itself, and each of its immediate children.
func candidates(root string) []string {
	out := []string{root}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, filepath.Join(root, entry.Name()))
		}
	}
	return out
}

// Entries renders local Skills for the per-Job plugin directory.
func Entries(local []Local) []Entry {
	out := make([]Entry, 0, len(local))
	for _, skill := range local {
		if skill.Available {
			out = append(out, Entry{Name: skill.Name, Path: skill.Path})
		}
	}
	return out
}

// describe reads the name and description from the SKILL.md front matter,
// falling back to the directory name.
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
