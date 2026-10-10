package adapter

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/skills"
)

// reportSkills tells Core which Skills exist only here. Core keeps the metadata
// and never the content, which is what lets a handoff say that another backend
// cannot run a given Skill (spec section 18).
func (a *Adapter) reportSkills(ctx context.Context) {
	sdk := a.sdk()
	if sdk == nil {
		return
	}

	local := make([]*backendv1.LocalSkill, 0, len(a.localSkills))
	for _, skill := range a.localSkills {
		local = append(local, &backendv1.LocalSkill{
			Name:        skill.Name,
			Description: skill.Description,
			Available:   skill.Available,
		})
	}
	if err := sdk.SendSkillInventory(ctx, local); err != nil {
		a.logger.Error("cannot report the local skills", slog.String("error", err.Error()))
	}
}

// prepareSkills makes the effective Skills of a Job available to the provider.
//
// Core-managed bundles that are not cached are fetched over the control stream
// and unpacked once; a Skill a backend already holds costs nothing. A Skill that
// cannot be fetched is skipped rather than failing the Job: the agent loses a
// capability, which is visible in its work, instead of the work not happening.
func (a *Adapter) prepareSkills(ctx context.Context, jobID string, declared []*backendv1.ProjectSkill) string {
	sdk := a.sdk()
	project := make([]skills.Entry, 0, len(declared))

	for _, skill := range declared {
		path := a.skillCache.Path(skill.GetSkillId(), skill.GetInstalledRevision())
		if !a.skillCache.Has(skill.GetSkillId(), skill.GetInstalledRevision()) {
			if sdk == nil {
				continue
			}
			bundle, err := sdk.FetchSkill(ctx, skill.GetSkillId(), skill.GetInstalledRevision(), skill.GetBundleSha256())
			if err != nil {
				a.logger.Error("cannot fetch a project skill",
					slog.String("skill", skill.GetName()), slog.String("error", err.Error()))
				continue
			}
			stored, err := a.skillCache.Store(skill.GetSkillId(), skill.GetInstalledRevision(), bundle)
			if err != nil {
				a.logger.Error("cannot cache a project skill",
					slog.String("skill", skill.GetName()), slog.String("error", err.Error()))
				continue
			}
			path = stored
			a.logger.Info("project skill cached",
				slog.String("skill", skill.GetName()),
				slog.String("revision", skill.GetInstalledRevision()))
		}
		project = append(project, skills.Entry{Name: skill.GetName(), Path: path})
	}

	// Project Skills come first: when a name collides, the Core-managed one is
	// the shared, reviewed definition.
	directory, err := skills.Assemble(
		filepath.Join(a.cfg.Provider.SkillCachePath, "jobs", jobID),
		project, skills.Entries(a.localSkills))
	if err != nil {
		a.logger.Error("cannot expose the skills of a job",
			slog.String("jobId", jobID), slog.String("error", err.Error()))
		return ""
	}
	return directory
}

// releaseSkills removes the per-Job plugin directory. It holds only symlinks
// into the cache, so nothing of value goes with it, and leaving one behind per
// Job would slowly fill the state volume.
func (a *Adapter) releaseSkills(jobID string) {
	directory := filepath.Join(a.cfg.Provider.SkillCachePath, "jobs", jobID)
	if err := os.RemoveAll(directory); err != nil {
		a.logger.Warn("cannot remove the skill directory of a finished job",
			slog.String("jobId", jobID), slog.String("error", err.Error()))
	}
}
