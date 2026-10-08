---
target: tasks page
total_score: 20
max_score: 40
na_heuristics: 
p0_count: 1
p1_count: 3
target_identity: "file:/home/thomas.betrancourt/Documents/Work/threavia/web/ui/src/routes/tasks.tsx"
target_fingerprint: "sha256:4b565a7ea4cf50aebf272a444d6c59028cb63dbab61c358738c3217108c02f37"
target_path: /home/thomas.betrancourt/Documents/Work/threavia/web/ui/src/routes/tasks.tsx
timestamp: 2026-10-08T20-59-23Z
slug: web-ui-src-routes-tasks-tsx
---
Method: dual-agent (A: design review · B: detector + browser evidence)

## Design Health Score

| # | Heuristic | Score | Key issue |
|---|---|---|---|
| 1 | Visibility of system status | 2 | Status buttons have no pending state; "show finished" shows nothing above the fold (Done group ~1450px down); in-progress task does not say who/where |
| 2 | Match system / real world | 2 | Actions are status nouns ("in progress", "done", "todo"), not verbs; "todo" on an in-progress row means "put back" |
| 3 | User control and freedom | 3 | Delete confirms; no undo; chip × removes instantly on a 20px target |
| 4 | Consistency and standards | 2 | Link buttons render 36px tall (size md overrides link variant); trash 28px; lowercase checkbox vs uppercase headings; same CircleDot for 3 states |
| 5 | Error prevention | 2 | "done" one click next to trash; mobile delete confirm overflows the card |
| 6 | Recognition rather than recall | 3 | Blockers named; dependency select flat 12 options, no search |
| 7 | Flexibility and efficiency | 1 | No shortcuts, no filter/search, no description or dependency at creation |
| 8 | Aesthetic and minimalist design | 2 | 4 controls repeated on every row; accent links louder than titles |
| 9 | Error recovery | 3 | ActionError used; dependency error lacks role=alert |
| 10 | Help and documentation | 0 | Nothing explains "ready" or that agents read/write tasks |
| **Total** | | **20/40** | **Acceptable (low)** |

## Design Specificity Verdict
LLM: generic CRUD list apart from the readiness split (In progress / Ready to start / Waiting on other work) and named blocker chips, which are specific and good. No link to sessions, jobs, backends or time. Below the Linear/Vercel register.
Detector: 0 findings (CLI on tasks.tsx and imported ui components; browser overlay desktop+mobile, light+dark). Clean scan does not cover touch targets: trash 28px, chip × 20px (below 24px AA), "Add a dependency" 24px. Muted text contrast passes (5.78:1 light, 6.28:1 dark). No console errors.

## Priority Issues
- [P0] Mobile row layout collapses: action cluster takes ~55% of 390px width, titles wrap one word per line; delete confirm overflows the card and overlaps the title. Fix: stack actions under the title below sm, confirm on its own line, 44px targets. (/impeccable adapt)
- [P1] Three end-of-row actions: lowercase status nouns as 36px link-buttons in accent + trash. Fix: leading status icon becomes the status control (menu: Start / Mark done / Back to to-do, one glyph per state), one overflow menu on the right (Add a dependency…, Delete…), optional one visible verb. (/impeccable distill)
- [P1] "show finished" checkbox: orphaned, lowercase, full-width click target, no visible effect. Fix: remove; always render a collapsed "Done · 3" section header at the bottom that expands in place, state remembered per project. (/impeccable distill)
- [P1] Add form dominates (largest element, only solid accent) and only takes a title. Fix: "New task" button in the header (N shortcut) opening an inline panel with title, details, waits-on; announce and highlight the filed task. (/impeccable layout)
- [P2] Row noise and inverted hierarchy: "+ Add a dependency" under every row (misaligned by 6px), titles regular weight, descriptions unclamped, waiting group unsorted. Fix: move to row menu, titles font-medium, line-clamp-2, sort waiting by depth, add age / working session meta. (/impeccable layout)

## Persona Red Flags
- Owner on the phone: unreadable titles, small targets next to delete, broken confirm.
- Owner at the desk after agent runs: no timestamps or session links, no keyboard path (~4 tab stops per row), "show finished" appears dead.
- Screen reader: status icons unnamed, buttons "done" without task context, silent group moves, dependency error not announced.

## Minor Observations
- Button variant link + default size md: root cause of oversized links.
- No loading/error state for readiness: TODO tasks flash into "Waiting" while it loads, and stay there silently on error.
- Empty state is plain text, not the dashed empty state of DESIGN.md.
- Dependency select 28px tall; "a task of another project" fallback vague.
- Done rows strikethrough + muted hurts legibility.

## Questions to Consider
- Agents file and close most tasks: should this page be a triage/steering surface (who is on it, what unblocks next) rather than a todo input?
- Should "Start" on a ready task offer to open a session on it?
- Should a waiting task be movable to in progress without a word, against the graph?
