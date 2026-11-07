# Layered Instructions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agent and supervisor instructions at the global, project and backend levels (plus session/job for the supervisor). Each level adds to the levels above it, the narrowest wins, and both the web UI and VS Code can edit them.

**Architecture:** Core stores the texts per level and assembles two labelled texts per job: agent and supervision. When several levels apply, a precedence preamble opens the text. Core sends them in the existing `ProjectContext.project_instructions` and `ExecutionPolicy.supervision` fields, so the backend protocol and the backends stay unchanged. Supervision leaves the instance/backend/project execution policies, so it is no longer replaced wholesale by `domain.Effective`.

**Tech Stack:** Go (Core, pgx, golang-migrate SQL), PostgreSQL, OpenAPI + openapi-typescript, React (web/ui), VS Code extension (TypeScript webviews).

**Spec:** `docs/superpowers/specs/2026-11-07-layered-instructions-design.md`

## Global Constraints

- Order, broadest first: **global → project → backend → session → job**. `LOCAL_INSTRUCTIONS` (backend env) stays appended last by the backend, unchanged.
- Layers only add. There is no "replace" mode.
- A level with an empty (after trim) text is skipped. 0 layers gives `""`. 1 layer gives its text verbatim, with no header or preamble. 2 or more layers give the preamble, then the `[Label]` sections.
- Preamble (exact copy): `These instructions come from several levels, broadest first. When two of them conflict, the later (narrower) level wins. Anything appended after them is narrower still.`
- Labels: `Global`, `Project <project name>`, `Backend <backend name>`, `Session`, `Job`.
- Max 4000 characters per new text (global agent/supervision, project supervision, backend agent/supervision). No retroactive limit on `projects.instructions`.
- Changes reach the **next** job. Changing instructions sends no `UpdateJobPolicy`.
- Delegated sessions (`session_create` from a manager) do **not** inherit the manager's supervision.
- The Threavia base system prompt text cannot be overridden.
- Every change happens in this worktree (`feat/layered-instructions`). Docker tests use a distinct compose project name, e.g. `-p threavia-layered-instr`.

## Review Focus

1. **A project policy that only changes the mode.** It must keep the global supervision in the reviewer text. This is the bug that motivated the work. Test: Task 4 `TestGlobalSupervisionSurvivesProjectMode`.
2. **Existing installations with supervision in instance/backend/project policies.** After migration that text must still reach the reviewer, now at its new level. Test: Task 2 `TestMigrationMovesSupervision`.
3. **Sessions whose own policy holds a copy of the inherited supervision**, created by "Override" pre-fill. The copy must not appear twice after migration. A different text must be kept. Test: Task 2 `TestMigrationStripsInheritedCopies`.
4. **Old clients PUTting a policy with `supervision` at instance/backend/project.** They must get a 400 with a message that points to `/instructions`, never a silent drop. Test: Task 3 `TestPolicySupervisionRejectedAboveSession`.
5. **Agent text with dynamic additions** (delegation note, handoff excerpts, MCP unavailability). These must stay after the layered sections and must not be swallowed into a label. Test: Task 4 `TestAgentInstructionsLayeredBeforeDynamicNotes`.

---

### Task 1: Domain assembler

**Files:**
- Create: `internal/core/domain/instructions.go`
- Test: `internal/core/domain/instructions_test.go`

**Interfaces:**
- Produces:
  - `type Instructions struct { Agent string \`json:"agent"\`; Supervision string \`json:"supervision"\` }`
  - `func (i Instructions) Validate() error`: each text at most 4000 chars.
  - `type Layer struct { Label, Text string }`
  - `const LayeredPreamble = "These instructions come from several levels, …"` (exact copy above)
  - `func Layered(layers []Layer) string`

- [ ] **Step 1: Write the failing tests**
  - `TestLayeredEmpty`: `Layered(nil) == ""` and `Layered([]Layer{{"Global","  "}}) == ""`.
  - `TestLayeredSingleIsVerbatim`: `Layered([]Layer{{"Global",""},{"Project p"," Use worktrees. "}}) == "Use worktrees."`.
  - `TestLayeredOrderAndPreamble`: with Global `A`, Project p `B` and Backend z `C`, the result equals `LayeredPreamble + "\n\n[Global]\nA\n\n[Project p]\nB\n\n[Backend z]\nC"`.
  - `TestInstructionsValidate`: 4001 characters gives an error, and 4000 gives none.
- [ ] **Step 2: Run** `go test ./internal/core/domain -run 'Layered|InstructionsValidate'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** in `instructions.go`. Trim each text and drop empty ones. Join the sections with `"\n\n"`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(core): assemble layered instructions`.

### Task 2: Schema, migration and store

**Files:**
- Create: `migrations/000037_layered_instructions.up.sql`, `.down.sql`
- Create: `internal/core/storage/postgres/instructions.go`
- Test: `internal/core/storage/postgres/instructions_test.go`. Follow the existing postgres test harness in that package (look at a sibling `_test.go` for how a DB is obtained and migrated to a given version).

**Interfaces:**
- Consumes: `domain.Instructions` (Task 1).
- Produces:
  - `func (s *Store) InstanceInstructions(ctx, ownerID domain.UserID) (domain.Instructions, error)`. Returns a zero value when there is no row.
  - `func (s *Store) SetInstanceInstructions(ctx, ownerID domain.UserID, in domain.Instructions) error`. Upserts the row.
  - `func (s *Store) ProjectInstructionsOf(ctx, ownerID domain.UserID, id domain.ProjectID) (domain.Instructions, error)`. `Agent` reads `projects.instructions`.
  - `func (s *Store) SetProjectInstructions(ctx, ownerID domain.UserID, id domain.ProjectID, in domain.Instructions) error`. Returns `ErrNotFound` when there is no row.
  - `func (s *Store) BackendInstructions(ctx, ownerID domain.UserID, id domain.BackendInstanceID) (domain.Instructions, error)`
  - `func (s *Store) SetBackendInstructions(ctx, ownerID domain.UserID, id domain.BackendInstanceID, in domain.Instructions) error`
  - `type JobLayers struct { ProjectName, BackendName string; Global, Project, Backend domain.Instructions; SessionSupervision, JobSupervision string }`
  - `func (s *Store) JobInstructionLayers(ctx, jobID domain.JobID) (JobLayers, error)`. Uses one query over jobs → runs → sessions → projects → backend_instances, plus a LEFT JOIN on instance_instructions by `p.owner_id`. Session and job supervision are read from `execution_policy->>'supervision'`.

**Migration (up):**
1. `CREATE TABLE instance_instructions (owner_id TEXT PRIMARY KEY, agent TEXT NOT NULL DEFAULT '', supervision TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`.
2. `ALTER TABLE projects ADD COLUMN supervision_instructions TEXT NOT NULL DEFAULT ''`. `ALTER TABLE backend_instances ADD COLUMN agent_instructions TEXT NOT NULL DEFAULT '', ADD COLUMN supervision_instructions TEXT NOT NULL DEFAULT ''`.
3. **Strip inherited copies first, while the old values are still in place.** For each session, compute the supervision it inherited under the old rules: the narrowest non-null of the instance, latest-run backend and project policies. If the session's own trimmed `execution_policy->>'supervision'` equals that value, remove the `supervision` key. Before that, insert an `audit_entries` row (`id gen_random_uuid()`, `owner_id`, `actor_id 'migration'`, `action 'instructions.migration_copy_removed'`, `session_id`, `detail jsonb_build_object('supervision', <text>)`). Apply the same rule to jobs, with the inherited value now including the session.
4. Move `execution_policy->>'supervision'` of `instance_execution_policies`, `backend_instances` and `projects` into the new columns or table. Then remove the key from those JSONB values (`execution_policy - 'supervision'`).

**Down:** move the texts back into the policy JSONB when the policy is non-null, then drop the new table and columns.

- [ ] **Step 1: Write the failing tests**
  - `TestInstructionsRoundTrip`: set and get at each of the three levels, plus owner isolation (another owner reads zero, and the backend/project setters return `ErrNotFound`).
  - `TestJobInstructionLayers`: fill every level, create a job, and check every `JobLayers` field, including names.
  - `TestMigrationMovesSupervision`: migrate to 36, write policies with `supervision` at instance/backend/project, migrate to 37. The texts are now in the new columns and the policies no longer have the key.
  - `TestMigrationStripsInheritedCopies`: session A's own supervision equals the project's, so it is removed and audited. Session B has a different text, so it is kept.
- [ ] **Step 2: Run** `go test ./internal/core/storage/postgres -run 'Instructions|Migration(Moves|Strips)'` (with `make dev-up` running). Expected: FAIL.
- [ ] **Step 3: Implement** the migration and `instructions.go`.
- [ ] **Step 4: Run** the same command. Expected: PASS. Also run `make migrate` against the dev DB, then the down migration and the up migration again.
- [ ] **Step 5: Commit** `feat(core): store instructions per level and move supervision out of default policies`.

### Task 3: Service and HTTP API for editing

**Files:**
- Create: `internal/core/service/instructions.go`
- Create: `internal/core/api/instructions.go` (handlers)
- Modify: `internal/core/api/knowledge.go:242-245`. Register the routes next to the policy routes.
- Modify: `internal/core/service/defaultpolicy.go` (`SetInstanceExecutionPolicy`, `SetBackendExecutionPolicy`) and `internal/core/service/policy.go` (project setter). Reject supervision there.
- Modify: `api/openapi.yaml`. Add the 3 paths and the `Instructions` and `InstructionsView` schemas. Document `supervision` on `ExecutionPolicy` as session/job only.
- Regenerate: `make web-generate vscode-generate`.
- Test: `internal/core/api/instructions_test.go`

**Interfaces:**
- Consumes: Task 1 and Task 2 store methods.
- Produces:
  - `type InstructionsView struct { Own domain.Instructions \`json:"own"\`; Inherited domain.Instructions \`json:"inherited"\` }`. `Inherited` is the global level rendered with `domain.Layered` for project/backend, and empty for global.
  - `func (s *Service) InstanceInstructions(ctx, identity) (InstructionsView, error)` / `SetInstanceInstructions(ctx, identity, domain.Instructions) (InstructionsView, error)`
  - `ProjectInstructions` / `SetProjectInstructions(ctx, identity, domain.ProjectID, …)`
  - `BackendInstructions` / `SetBackendInstructions(ctx, identity, domain.BackendInstanceID, …)`
  - Routes `GET|PUT /api/v1/me/instructions`, `/api/v1/projects/{projectId}/instructions`, `/api/v1/backends/{backendId}/instructions`.
  - Audit actions `instructions.instance_set`, `instructions.project_set`, `instructions.backend_set`.
  - Error text for a policy with supervision above session: `supervision is set with the instructions now: use /instructions` (wrapped in `ErrInvalid` → 400).

- [ ] **Step 1: Write the failing tests** (harness: `setup(t)`, `c.mustDo`, `c.asUser`)
  - `TestInstructionsEndpoints`: PUT/GET at all three levels. Check that `inherited.supervision` on project and backend equals the global text. Check that the project `agent` written here is visible through `GET /api/v1/projects/{id}` `instructions`. Check that another user gets 404 on the project and backend.
  - `TestInstructionsTooLong`: 4001 characters gives 400.
  - `TestPolicySupervisionRejectedAboveSession`: PUT `/me/policy`, `/backends/{id}/policy` and `/projects/{id}/policy` with `supervision` gives 400. A session policy with `supervision` gives 200.
  - Update `TestInstanceBackendProjectPolicyInheritance` (`defaultpolicy_test.go:28`): drop `backendPolicy["supervision"]`.
- [ ] **Step 2: Run** `go test ./internal/core/api -run 'Instructions|PolicySupervision|PolicyInheritance'`. Expected: FAIL.
- [ ] **Step 3: Implement** the service, the handlers, the routes, the OpenAPI changes, and regenerate.
- [ ] **Step 4: Run** the same tests, then `make web-generate-check`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): edit agent and supervisor instructions per level`.

### Task 4: Assemble per job, and fix delegation

**Files:**
- Modify: `internal/core/service/jobs.go`. Around line 133 (dispatch), and in `projectContext` (line ~279), where the base becomes the assembled agent text instead of `jc.ProjectInstructions`.
- Modify: `internal/core/service/policy.go:146` (`pushPolicy`). Assemble the supervision before `policyToProto`.
- Modify: `internal/core/service/sessiontools.go:592`. Set `policy.Supervision = ""` before the policy is copied into the delegated session.
- Test: `internal/core/api/instructions_job_test.go`

**Interfaces:**
- Consumes: `Store.JobInstructionLayers`, `domain.Layered`.
- Produces: `func (s *Service) jobInstructions(ctx, jobID domain.JobID) (agent, supervision string, err error)`.
  - The agent layers are Global, Project, Backend.
  - The supervision layers are Global, Project, Backend, Session (`SessionSupervision`), Job (`JobSupervision`).
  - Both dispatch and `pushPolicy` overwrite `policy.Supervision` with the assembled supervision. Leave `domain.Effective` unchanged: after Task 2, only session/job policies carry supervision, so views keep showing only the session's own text.

- [ ] **Step 1: Write the failing tests**
  - `TestAgentInstructionsLayeredBeforeDynamicNotes`: set global agent `G` and project agent `P`, then start a session. `ProjectContext.ProjectInstructions` starts with `LayeredPreamble` and contains `[Global]\nG` before `[Project <name>]\nP`. A delegated child job's text contains the delegation note *after* the sections.
  - `TestAgentInstructionsSingleLayerUnchanged`: only project instructions are set, so the text equals them exactly, as today.
  - `TestGlobalSupervisionSurvivesProjectMode`: set global supervision `S`, then PUT a project policy with only `mode: SUPERVISED`. The started job's `ExecutionPolicy.Supervision` equals `S`.
  - `TestSupervisionLayeredWithSession`: global `S1`, backend `S2` and a session policy with supervision `S3` give a supervision with the sections Global, Backend <name>, Session, in that order. A session policy change pushes `UpdateJobPolicy` with the same assembled text.
  - `TestDelegatedSessionDoesNotInheritManagerSupervision`: the manager's project B has supervision `M`. The child in project A, with supervision `A`, gets `A` only, and its stored session policy has no `supervision`.
- [ ] **Step 2: Run** `go test ./internal/core/api -run 'AgentInstructions|Supervision(Survives|Layered)|DelegatedSession'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same tests, then `make test`. Expected: PASS. The existing `resume_test.go` instructions assertions must still pass.
- [ ] **Step 5: Commit** `feat(core): send layered agent and supervisor instructions to every job`.

### Task 5: MCP project tools

**Files:**
- Modify: `internal/core/tools/manage.go:239-256`. Add a `supervisionInstructions` input to `project_update` ("Its new supervisor instructions, in full.") and mention it in the `project_read` description.
- Modify: `internal/core/service/managetools.go:468` (read entry) and `:519-533` (update). When the field is present, call `SetProjectInstructions` with the current agent text and the new supervision, under the same person validation as `instructions`.
- Test: extend `internal/core/api/managetools_test.go`.

- [ ] **Step 1: Write the failing test** `TestManageProjectSupervisionInstructions`: `project_update` with `supervisionInstructions` after validation, then `project_read` returns it.
- [ ] **Step 2: Run** `go test ./internal/core/api -run ManageProjectSupervision`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** it again. Expected: PASS.
- [ ] **Step 5: Commit** `feat(tools): read and update a project's supervisor instructions`.

### Task 6: Web UI

**Files:**
- Create: `web/ui/src/components/instructions-panel.tsx`. Export `InstructionsPanel({ scope }: { scope: { kind: 'instance' } | { kind: 'project'; projectId: string } | { kind: 'backend'; backendId: string } })`. It has two labelled textareas: "Agent instructions" and "Supervisor instructions", the latter with a hint that only a SUPERVISED reviewer reads it. Under each one, the `inherited` text in a read-only muted block titled "Inherited from above". It uses the same edit/save/saved pattern as `default-policy.tsx`, and a `maxLength` of 4000.
- Modify: `web/ui/src/api/queries.ts`. Add `useInstructions(scope)` and `useSetInstructions(scope)` on the `/instructions` paths. The mutation invalidates `['instructions']`, `['project']` and `['audit']`.
- Modify: `web/ui/src/routes/settings.tsx:106` and `:305`. Render `InstructionsPanel` next to `DefaultPolicyPane` (instance tab and backend page).
- Modify: `web/ui/src/routes/project.tsx`, `InstructionsView` (~198). Keep the existing agent editor, which already edits `projects.instructions`. Below it, add the supervisor textarea and the inherited blocks by rendering `InstructionsPanel` in project scope, restricted to the supervision field, or by merging the two. The builder picks whichever reads better, as long as there is a single save path per field.
- Modify: `web/ui/src/components/policy-form.tsx:155`. Add a `showSupervision?: boolean` prop, default `false`. Pass `true` only from `policy-panel.tsx` (session).
- Modify: `web/ui/src/demo/server.ts`. Answer the three `/instructions` routes.

- [ ] **Step 1:** Implement, then run `make web-lint web-demo-check`. Expected: PASS.
- [ ] **Step 2:** In a docker-compose environment from the worktree (`docker compose -p threavia-layered-instr up -d --build`), save instructions at each level. Check the inherited block and the absence of the supervision field in the project, backend and instance policy forms. Publish a screenshot.
- [ ] **Step 3: Commit** `feat(web): edit layered instructions`.

### Task 7: VS Code

**Files:**
- Create: `clients/vscode/src/instructions/editor.ts` and `clients/vscode/src/instructions/page.ts`, modelled on `src/permissions/editor.ts` and `page.ts`. The page shows the two textareas (maxlength 4000) and the inherited text read-only, and saves with `PUT <path>`.
- Modify: `clients/vscode/src/extension.ts`. Add the command `threavia.manageInstructions`, copying the target resolution of `threavia.managePermissions` (line ~357) with the scopes `Global`, `Backend` and `Project`, and paths `/api/v1/me|backends/{id}|projects/{id}/instructions`.
- Modify: `clients/vscode/package.json`. Declare the command (title "Instructions…", category Threavia) and add it to the same context menus as `threavia.managePermissions`, for project and backend nodes.
- Modify: `clients/vscode/src/permissions/page.ts:25,54`. Show the supervision label only when the path is a session path, even in SUPERVISED mode.
- Test: `clients/vscode/test/instructions.test.ts`. Follow `permissions.test.ts`: the page's save posts `{agent, supervision}` to the right path, and the inherited text renders escaped.
- Test: update `clients/vscode/test/permissions.test.ts` for the hidden supervision field at the project, backend and instance levels.

- [ ] **Step 1: Write the failing tests** above.
- [ ] **Step 2: Run** `make vscode-check`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `make vscode-check`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(vscode): edit layered instructions`.

### Task 8: Docs and final check

**Files:**
- Modify: `docs/architecture.md`, in the policy section that mentions `me/policy`. Describe the instruction levels, their order, and that supervision is no longer part of instance/backend/project policies.
- Modify: the `Supervision` doc comment in `internal/core/domain/policy.go:172-188`. It now carries only the session/job layer, and Core replaces it with the assembled text on the wire.

- [ ] **Step 1:** Edit the docs.
- [ ] **Step 2:** Run `make fmt lint test web-lint web-generate-check web-demo-check vscode-check`. Expected: all pass.
- [ ] **Step 3: Commit** `docs: layered instructions`.
