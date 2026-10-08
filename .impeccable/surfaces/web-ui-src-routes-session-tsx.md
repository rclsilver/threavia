---
version: 1
slug: "web-ui-src-routes-session-tsx"
primary_target: "web/ui/src/routes/session.tsx"
related_targets: ["web/ui/src/components/app-shell.tsx","web/ui/src/components/attention.tsx","web/ui/src/components/timeline.tsx"]
---

# Surface brief — the web client shell and session view

Scope: app shell (sidebar, mobile drawer), session view (timeline, composer, attention), and a new Waiting inbox. Mode: Operate.
Audience and job: the owner at the desk following agents, and on the phone deciding approvals and questions quickly and safely (PRODUCT.md).
Constraints: English UI, existing mark kept, WCAG 2.2 AA, 44px touch targets on the phone, light/dark from the OS. Code-led (no image generation available).
Scope of this pass (user's choice after the 2026-10-08 critique): the three P1s — approval card, reaching what waits, silent failures — inside the chosen register.

## Direction contract

THESIS: The category standard played straight — a clean working product at Linear/Vercel craft — whose session reads as an operations log containing the conversation. Refuses the ChatGPT clone: pill composer, gradient send, chat bubbles, a log hidden inside a chat.

OWN-WORLD: Neutral cool-grey ground and surfaces, hairline 1px borders, one indigo accent for primary actions and selection, state colours only where they mean something (green done, amber waiting, red danger) with text-safe variants; system UI type, tabular numerals for every time, duration and cost, monospace only for commands, paths and code; 6–8px radii, no gradients, no glass.

STORY: The owner sees at once what waits for them across every project, reads exactly what an approval would run, on which machine, in which directory and at what risk, decides with a thumb, and sees that the decision landed; nothing they do fails without saying so.

FIRST VIEWPORT: Session view — header (title, status, backend), the log: each row a timestamp gutter, then the entry (agent prose unmarked, the person's messages as tinted rows labelled You, tool calls as one-line rows); above the composer, the approval card: headline command in mono, context strip (backend · directory · capability with risk), full-request disclosure, Deny and Approve as large separated buttons. Composer: a squared panel with a toolbar row (delivery choice, Stop), flat accent Send.

FORM: canon (category standard), chosen by the user over rolled directions; position: standing exit; seed key b78f18d4.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
