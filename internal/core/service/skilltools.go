package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/tools"
)

// FileInput is supported by existing backends. Stay below gRPC's default
// 4 MiB message limit, including the request envelope, without changing them.
const maxSkillImportBytes = 3 << 20

func (s *Service) skillTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any, file []byte) (map[string]any, error) {
	fields := map[tools.Name]string{
		"skill_list": "", "skill_read": "skillId file",
		"skill_import":      "path filename name subdirectory scope expectedRevision",
		"skill_set_enabled": "skillId enabled", "skill_make_global": "skillId",
		"skill_delete": "skillId expectedRevision",
	}
	for key, value := range input {
		if !strings.Contains(" "+fields[name]+" ", " "+key+" ") || key == "" {
			return nil, fmt.Errorf("%w: unsupported field %s", ErrInvalid, key)
		}
		if key == "enabled" {
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("%w: enabled must be a boolean", ErrInvalid)
			}
		} else if v, ok := value.(string); !ok || len(v) > 4096 {
			return nil, fmt.Errorf("%w: %s must be a string up to 4096 bytes", ErrInvalid, key)
		}
	}
	if name == "skill_list" {
		project, err := s.ListSkills(ctx, identity, jc.ProjectID)
		if err != nil {
			return nil, err
		}
		global, err := s.ListGlobalSkills(ctx, identity)
		if err != nil {
			return nil, err
		}
		if project == nil {
			project = []domain.Skill{}
		}
		if global == nil {
			global = []domain.Skill{}
		}
		return sessionToolResult(map[string]any{"project": project, "global": global, "applies": "next Job"})
	}
	if name == "skill_import" {
		if text(input, "path") == "" || len(file) == 0 || len(file) > maxSkillImportBytes {
			return nil, fmt.Errorf("%w: provide an archive from this backend, up to 3 MiB", ErrInvalid)
		}
		projectID, err := memoryProject(jc.ProjectID, text(input, "scope"))
		if err != nil {
			return nil, err
		}
		expected := text(input, "expectedRevision")
		installed, err := s.installSkill(ctx, identity, projectID, domain.SkillSource{
			Type: domain.SkillSourceUpload, URL: text(input, "filename"), Path: text(input, "subdirectory"),
		}, text(input, "name"), bytes.NewReader(file), &expected)
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"skill": installed, "applies": "next Job"})
	}
	id := text(input, "skillId")
	if !isUUID(id) {
		return nil, fmt.Errorf("%w: skillId must be a UUID", ErrInvalid)
	}
	current, err := s.store.GetSkill(ctx, identity.UserID, domain.SkillID(id))
	if err != nil {
		return nil, translate(err)
	}
	if current.ProjectID != "" && current.ProjectID != jc.ProjectID {
		return nil, ErrNotFound
	}
	switch name {
	case "skill_read":
		files, err := s.SkillFiles(ctx, identity, current.ID)
		if err != nil {
			return nil, err
		}
		requested := text(input, "file")
		if requested == "" {
			requested = "SKILL.md"
		}
		var selected any
		inventory := make([]any, 0, len(files))
		for _, f := range files {
			inventory = append(inventory, map[string]any{"path": f.Path, "size": f.Size, "readable": f.Text != nil})
			if f.Path == requested {
				selected = f
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("%w: file is not in the skill", ErrNotFound)
		}
		return sessionToolResult(map[string]any{"skill": current, "files": inventory, "file": selected})
	case "skill_make_global":
		moved, err := s.MakeSkillGlobal(ctx, identity, current.ID)
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"skill": moved, "applies": "next Job"})
	case "skill_set_enabled":
		enabled, ok := input["enabled"].(bool)
		if !ok {
			return nil, fmt.Errorf("%w: enabled is required", ErrInvalid)
		}
		if err := s.SetSkillProjectEnabled(ctx, identity, jc.ProjectID, current.ID, enabled); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true, "applies": "next Job"}, nil
	case "skill_delete":
		expected := text(input, "expectedRevision")
		if expected == "" {
			return nil, fmt.Errorf("%w: expectedRevision is required", ErrInvalid)
		}
		if err := s.uninstallSkill(ctx, identity, current.ID, &expected, &current.ProjectID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "applies": "next Job"}, nil
	}
	return nil, ErrInvalid
}
