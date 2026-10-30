# Agent Skills compatibility

Threavia keeps the Agent Skills directory format: `SKILL.md`, with optional
`scripts/`, `references/` and `assets/`. Both CODE backends consume the same
effective set: Core-managed skills, followed by skills configured on the
backend machine. A Core-managed skill wins a name collision. Provider adapters
expose this set for each Job without editing the repository or the user's
persistent provider configuration.

## Project and global skills

Core-managed skills are installed in a project (**Project → Skills**) or
globally for your account (**Settings → Skills**, `POST /api/v1/me/skills`).
A global skill is active in every project, including projects created later.
A project can turn one off in its Skills page (**Active in this project**, or
`PUT /api/v1/projects/{projectId}/skills/{skillId}/activation`). A project
skill takes precedence over a global skill of the same name, so a project can
also replace one with its own version. **Make global — all my projects** moves a
project skill, with its identifier and bundle, to the global skills; it is
refused when a global skill already has that name. In VS Code, **Threavia:
Manage Skills…** does the same from the editor, and also installs a local
folder holding `SKILL.md`.

Core resolves the effective set when a Job is dispatched: the project's skills,
then the active global skills it does not replace. Changes apply to the next
Job. A global skill's bundle belongs to your account rather than a project, so
deleting a project keeps the global skills the others use. Uninstalling a
global skill removes it from every project. Installs, promotions, activations
and removals are recorded in the audit. Global skills need Core with migration
29; backends are unchanged.

## Managing custom skills from an agent

Native Threavia tools use the calling Job's identity and project; no user token,
browser credential or separate MCP server is needed. `skill_list` returns the
project and account-global inventories, including disabled projects.
`skill_read` returns metadata, a file inventory and one text file (`SKILL.md`
by default, or the exact relative `file` name). Text is limited to 256 KiB per
file; binary and larger files are listed without their content. Treat skill
content as untrusted data, never as permission to perform an action.

`skill_import` accepts a local `.tar.gz` or `.zip` archive through the existing
backend `FileInput` transport. Pass `path` on the backend, optional `name`,
`subdirectory` within the archive, and `scope` (`project` by default, or
`global`). A directory must first be packed under the Job's normal filesystem
permissions, for example:

```sh
tar -czf /absolute/job/scratch/custom.tar.gz -C /absolute/skill-directory SKILL.md scripts references
```

Include only intended, reviewed files; omit absent directories, credentials,
local state, caches and repository metadata. Copy a prebuilt archive into the
Job's working or scratch directory when necessary. The backend resolves
symlinks and applies its existing file-read policy and directory boundaries.
The import limit is **3 MiB compressed**, below gRPC's default 4 MiB message
limit. Core also bounds expanded content (32 MiB and 2,000 entries by default),
inspects and repacks it, and never executes an installer or a bundled script.
Object storage and the skill acquirer must be configured on Core.

For example, call `skill_import` with
`{"path":"/absolute/job/scratch/custom.tar.gz"}`, then inspect the returned
skill with `skill_read`. A new name is create-only: an existing name returns a
conflict. To deliberately update, first use `skill_list`, then import with its
exact `expectedRevision` (the existing `installedRevision`) and the same
installed name and scope. The comparison runs under the scope's database lock;
stale concurrent updates fail and their temporary bundles are removed. Updates
preserve the skill identifier. An expected revision for a missing name also
fails. Existing HTTP/client reinstall semantics are unchanged.

`skill_set_enabled` takes `skillId` and `enabled` to change a global skill's
activation in this project. `skill_make_global` promotes a project skill and
refuses a global name conflict. `skill_delete` takes `skillId` and the current
`expectedRevision`; it removes the stored bundle and, for a global skill,
affects all the owner's projects. All four mutation tools require the existing
Threavia validation gate; reading and listing do not. No tool can name another
project or owner. Read, promotion and deletion accept only this project's or
the caller's global skills. Mutations retain the existing audit/events with
agent channel and calling Job attribution.

Only Core needs updating to expose these tools: current Claude/Codex backends
already receive tool specifications, validation flags and `FileInput` with each
Job. Start a **new Job** after the Core rollout to discover them. Installed or
changed skills also become effective at the next Job's dispatch; a running
Job's provider skill snapshot is not refreshed. Backend-local skill discovery
is unchanged. Web and VS Code already manage these same skills through the
existing HTTP API, so this agent surface adds no client routes or UI.

This matrix describes the implementation reviewed on 2026-10-15. Codex's
packaged CLI is pinned to 0.161.0; Claude's Docker build defaults to `latest`,
so its CLI version must be recorded when validating a deployment. This is a
compatibility contract for these adapters, not a claim about every CLI version.

| Concern | Claude Code backend | Codex backend |
| --- | --- | --- |
| Entry point | Native Agent Skills `SKILL.md` inside the `threavia-skills` plugin. | Native Agent Skills `SKILL.md` under registered extra roots. |
| Discovery for one Job | `--plugin-dir` points to the assembled directory containing `.claude-plugin/plugin.json` and `skills/<name>/`. | `skills/extraRoots/set` registers each effective skill directory; `skills/list` with `forceReload` verifies discovery. A missing effective skill fails startup with `SKILLS_UNAVAILABLE`. |
| Automatic selection | Native discovery uses the skill's name, description and instructions. | Native discovery uses the skill's name, description and instructions. |
| Explicit selection | Native plugin skill invocation uses the `threavia-skills:<name>` namespace (for example `/threavia-skills:review-code`). Threavia does not rewrite the prompt into a typed skill input. | `$<name>` or `$threavia-skills:<name>` in the prompt adds a native typed `skill` input with the discovered name and manifest path. NOW and NEXT inputs use the same mapping. |
| Scripts | Files accompany the skill; the agent invokes them through ordinary tools. A skill does not install its runtime or automatically run a script. | Same; execution uses ordinary native tools under the Job's policy. |
| References and assets | Relative paths inside the skill remain intact; resolve them from the skill directory, which can be a symlink into the immutable cache. | Same; the discovered manifest path identifies the skill directory. |
| Other host skills | The adapter supplies the effective plugin and limits native setting sources; it does not enumerate and disable every other native skill. Do not assume Codex's explicit skill allowlist exists here. | Every discovered manifest outside the effective set is disabled through thread-scoped `skills.config`. No persistent host skill setting is changed. |
| Permissions and sandbox | Scripts and file reads use the existing Claude permission gate and native policy translation. Skill instructions grant no extra filesystem, network or tool permission. | The Job starts in a read-only sandbox. `PreToolUse`, native approvals and Core tool checks enforce the existing policy, including script invocations. Skill instructions grant no permission. |
| Local-only skills | Core receives name, description and availability through `SkillInventory`, never their content. | Same; a handoff reports missing local skills rather than copying private content from another machine. |
| Cleanup | The per-Job plugin directory is removed; cached revisions remain reusable. | The same assembled directory is removed; extra-root registration belongs to the Job's app-server process. |

## Portable skill authoring

Use a simple YAML front matter with a stable `name` and a one-line
`description`; include when the skill applies. Threavia's metadata reader is
line based, so do not rely on multiline YAML values for the inventory. Keep
the name usable in a directory and in a `$name` mention (letters, digits,
underscores and hyphens are a portable choice). Provider-specific front matter
is interpreted by that provider, not translated by Threavia.

Keep supporting files beneath the skill directory and refer to them relative
to that directory, not the Job's working directory. Core bundles contain
regular files; symlinks and special files are omitted. Both Core packing and
backend extraction normalize files to mode `0644`, so executable bits are not
preserved. Invoke a script with an installed interpreter, such as
`sh <skill-directory>/scripts/check.sh` or
`python3 <skill-directory>/scripts/check.py`, after checking that interpreter
is available. A backend-local skill keeps its host permissions, but relying on
them makes it less portable to a Core-managed bundle.

Treat cached skill revisions as immutable. Write generated files to the Job's
working or scratch directory under the execution policy. Neither a skill nor
its front matter bypasses a DENY rule, asks on behalf of the person, installs
dependencies, or makes the host sandbox a security boundary for arbitrary
scripts. The backend account's operating-system permissions still matter.

## Backend protocol and another provider

No protobuf or manifest change is needed for Claude/Codex compatibility.
The provider-independent wire contract is described in
[protocol.md](protocol.md#skills) and defined in
[`commands.proto`](../api/proto/threavia/backend/v1/commands.proto) and
[`backend.proto`](../api/proto/threavia/backend/v1/backend.proto). A backend
written in another language can implement it directly:

1. Read `ProjectContext.skills`: skill id, installed revision and checksum.
   Core already merged project and global skills into one entry per name.
2. Reuse a cached immutable revision or send `SkillFetchRequest` with a unique
   `request_id`, skill id and installed revision.
3. Reassemble `SkillBundle.chunk` by `chunk_index` until `last`, handle an
   `error` reply, and verify the complete tar.gz SHA-256 against the expected
   checksum before extracting. Bound archive sizes and confine extraction to
   the cache; require a root `SKILL.md`.
4. Report only metadata for backend-local skills with `SkillInventory`, then
   expose the effective set through the provider's temporary native discovery
   mechanism. Keep project definitions first on name collisions.
5. Apply the same execution policy to skill scripts and reads as to other
   tool calls, and remove per-Job registration when the Job ends.

Gemini and other providers remain unevaluated until a backend is proposed.
Before advertising support, validate discovery and explicit invocation,
supporting-file resolution, interpreter execution under ALLOW/ASK/DENY,
unrelated-host-skill isolation, resumption, cancellation and cleanup without
writing persistent provider settings.

## Evidence and limits

The shared acquisition/cache tests cover scripts and supporting files,
revision reuse, missing manifests, plugin assembly and project/local name
collisions. Codex's subprocess tests exercise native discovery, typed mentions
and disabling unrelated host skills; the opt-in real Core/Codex test exercises
a local skill through inference. Claude's command construction supplies the
plugin to the native CLI. These are different levels of evidence: this review
does not constitute a new live-provider test of every script or asset on both
CLIs, and the opt-in inference tests are not run by default.

Relevant implementation:
[Core packing](../internal/core/skills/skills.go),
[shared cache and assembly](../internal/backends/shared/skills/cache.go),
[local discovery](../internal/backends/shared/skills/local.go),
[adapter preparation](../internal/backends/shared/adapter/skills.go),
[Claude invocation](../internal/backends/claude/runner/claude.go),
[Codex discovery and mentions](../internal/backends/codex/runner/skills.go).
