# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

One technical owner of their own infrastructure (a homelab and a work machine) who drives coding agents — Claude Code today — running on their own machines. Two situations, both primary:

- **At the desk, large screen:** start work, follow it live, read what the agent did (messages, tool calls, diffs), manage the project's memory and permissions.
- **Away from the desk, on the phone (installed PWA):** answer an approval or a question quickly and without mistakes, then put the phone away. Notifications bring them back to exactly what waits.

Teams, shared projects and roles are planned but are not the current target.

## Product Purpose

Threavia is a self-hosted control plane for coding agents. It owns durable projects, sessions, jobs, events and project knowledge, while execution stays on autonomous backends (a laptop running Claude Code, later a pod running Codex). Success: the owner can leave the agent working, be reached only when a decision is theirs to make, decide it safely from wherever they are, and find later what was done and why.

## Positioning

- **A Session outlives the agent's own session:** it survives the client (web, phone, later VS Code and voice) and the machine (an explicit move to another backend).
- **Execution stays on the owner's machines.** Core never holds provider credentials or sees the filesystem; diffs are fetched from the backend on demand.
- **Project memory is provider-independent:** tasks, decisions and searchable history belong to the project, not to a provider session.
- **Explicit rather than magic:** no silent migration, a readable execution policy enforced by Threavia, refusals rather than advice.

Interface consequence: the person must always see **which machine** is doing **what**, **where**, and **what waits for their decision**.

## Operating Context

- Core runs in the owner's Kubernetes cluster; backends run as a NixOS service under the owner's account on their machines.
- Core loop: open a Session on a backend and a directory, send a message, watch a live timeline (agent messages, tool calls, file changes), answer validations (permission requests) and questions, stop or redirect the running Job (Queue / Next step / Interrupt), schedule recurring messages.
- Execution policy modes: INTERACTIVE, GUARDED, SUPERVISED (a reviewer model answers in the user's place), AUTONOMOUS; switches for write, commit, push, network; per-project and per-session rules.
- Notifications: Web Push to subscribed devices, only when the person is not active on another client.
- Terminology used in the product and API: Project, Session, Run, Job, Backend (BackendInstance), KnownDirectory, Validation, Question (user input), Policy, Schedule, Artifact, Skill, Decision, Task.

## Capabilities and Constraints

- Web client: React + Tailwind, served by Core from the same binary; must work on desktop and phone (installable PWA, Web Push).
- The UI is in English.
- Single user per deployment today; isolation per owner is enforced server-side.
- Undecided: validation levels (INFO / CONFIRM / SENSITIVE) and which decisions may be taken from a notification or by voice — to be settled with the Voice work.

## Brand Commitments

- Name: Threavia.
- The existing mark and icon (`brand/threavia-icon.png`, `web/ui/src/components/logo.tsx`, `web/ui/public/icon-*.png`) are kept as they are.
- Light and dark themes follow the operating system.
- Visual register (chosen 2026-10-08 over rolled directions): the category standard played straight, without irony or smuggled quirk — a clean working product at the craft level of Linear and Vercel. The session timeline is organised as an operations log that contains the conversation, not a chat that contains logs.

## Evidence on Hand

- `THREAVIA_SPEC_V1.md` (the architecture and V1 contract) and `README.md`.
- No customers, testimonials or metrics exist; none may be invented.

## Product Principles

1. **The decision is the product.** The moments where the person must decide — approve, answer, stop — get the most clarity, context and safety, especially on a phone.
2. **Always say which machine, what, where.** Machine, directory, capability and risk are first-class information, not details.
3. **Explicit over magic.** Nothing moves, retries, loosens or is sent late without saying so where the person looks.
4. **Converge on one truth.** Every client shows the same state; something answered elsewhere stops being actionable here.
5. **Readable as a record.** What happened can be read back later as an operations log, not reconstructed from memory.

## Accessibility & Inclusion

Target WCAG 2.2 AA. Touch targets of at least 44px on the phone; status never conveyed by colour alone; live changes announced to assistive technology.
