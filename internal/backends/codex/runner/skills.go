package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type nativeSkill struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// Register the effective per-Job skills in native discovery, and explicitly
// disable skills discovered from unrelated host/repository configuration.
// No persistent Codex config or repository file is changed.
func loadSkills(ctx context.Context, r *rpc, cwd, directory string, config map[string]any) ([]nativeSkill, error) {
	roots := []string{}
	expected := map[string]bool{}
	if directory != "" {
		root, err := filepath.Abs(filepath.Join(directory, "skills"))
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("read effective skills: %w", err)
		}
		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			manifest := filepath.Join(path, "SKILL.md")
			if _, err := os.Stat(manifest); err != nil {
				return nil, fmt.Errorf("read skill %s: %w", entry.Name(), err)
			}
			roots = append(roots, path)
			expected[canonicalPath(manifest)] = true
		}
	}
	if err := r.call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": roots}, nil); err != nil {
		return nil, fmt.Errorf("register native skills: %w", err)
	}
	var list struct {
		Data []struct {
			Skills []nativeSkill                    `json:"skills"`
			Errors []struct{ Path, Message string } `json:"errors"`
		} `json:"data"`
	}
	if err := r.call(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}, &list); err != nil {
		return nil, err
	}
	var effective []nativeSkill
	settings := []any{}
	seen := map[string]bool{}
	for _, group := range list.Data {
		for _, skill := range group.Skills {
			path := canonicalPath(skill.Path)
			if seen[path] {
				continue
			}
			seen[path] = true
			enabled := expected[path]
			settings = append(settings, map[string]any{"path": skill.Path, "enabled": enabled})
			if enabled {
				skill.Enabled = true
				effective = append(effective, skill)
			}
		}
	}
	for path := range expected {
		if !seen[path] {
			return nil, fmt.Errorf("Codex did not discover effective skill %s; check its SKILL.md metadata", path)
		}
	}
	config["skills.config"] = settings
	return effective, nil
}

func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

var skillMention = regexp.MustCompile(`(?:^|\s)\$([A-Za-z0-9_:.\-]+)`)

func skillInput(text string, skills []nativeSkill) []any {
	input := []any{map[string]any{"type": "text", "text": text}}
	seen := map[string]bool{}
	for _, mention := range skillMention.FindAllStringSubmatch(text, -1) {
		name := strings.TrimPrefix(mention[1], "threavia-skills:")
		for _, skill := range skills {
			if skill.Name == name && !seen[name] {
				input = append(input, map[string]any{"type": "skill", "name": skill.Name, "path": skill.Path})
				seen[name] = true
			}
		}
	}
	return input
}
