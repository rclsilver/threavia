---
name: Threavia
description: A clean working client for AI agents that run on your own machines; the session reads as an operations log, and every decision is made with full context, by thumb if need be.
colors:
  accent: "oklch(52% 0.19 275)"
  accent-text: "oklch(99% 0 0)"
  accent-2: "oklch(58% 0.21 310)"
  canvas: "oklch(98.6% 0.002 265)"
  surface: "oklch(100% 0 0)"
  surface-2: "oklch(96.4% 0.003 265)"
  border: "oklch(91.5% 0.004 265)"
  text: "oklch(22% 0.007 265)"
  muted: "oklch(50% 0.01 265)"
  ok: "oklch(55% 0.14 155)"
  warn: "oklch(68% 0.15 75)"
  warn-text: "oklch(49% 0.11 65)"
  danger: "oklch(55% 0.2 25)"
  danger-solid: "oklch(55% 0.2 25)"
  accent-dark: "oklch(70% 0.15 275)"
  accent-text-dark: "oklch(17% 0.012 265)"
  accent-2-dark: "oklch(72% 0.17 310)"
  canvas-dark: "oklch(15.5% 0.003 265)"
  surface-dark: "oklch(19.5% 0.004 265)"
  surface-2-dark: "oklch(24% 0.005 265)"
  border-dark: "oklch(29% 0.006 265)"
  text-dark: "oklch(94% 0.004 265)"
  muted-dark: "oklch(66% 0.008 265)"
  ok-dark: "oklch(72% 0.16 155)"
  warn-dark: "oklch(80% 0.14 80)"
  warn-text-dark: "oklch(80% 0.14 80)"
  danger-dark: "oklch(68% 0.18 22)"
  danger-solid-dark: "oklch(50% 0.19 25)"
typography:
  headline:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
    lineHeight: 1.556
  title:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 600
  prose:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 400
    lineHeight: 1.625
  body:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.43
  log-line:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: "1.25rem"
  meta:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.333
  label:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.75rem"
    fontWeight: 500
    letterSpacing: "0.025em"
  badge:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "0.6875rem"
    fontWeight: 500
  mono:
    fontFamily: "ui-monospace, 'SF Mono', 'JetBrains Mono', Menlo, Consolas, monospace"
    fontSize: "0.875rem"
    fontWeight: 400
  gutter:
    fontFamily: "ui-monospace, 'SF Mono', 'JetBrains Mono', Menlo, Consolas, monospace"
    fontSize: "0.6875rem"
    fontWeight: 400
    lineHeight: "1.25rem"
    fontFeature: "'tnum'"
rounded:
  sm: "4px"
  md: "6px"
  card: "8px"
  full: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  reading: "46rem"
  page: "64rem"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-text}"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-secondary:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-ghost:
    textColor: "{colors.muted}"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-ghost-hover:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text}"
  button-danger:
    backgroundColor: "{colors.danger-solid}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-sm:
    height: "32px"
    padding: "0 10px"
  button-icon:
    size: "32px"
  button-lg-phone:
    height: "44px"
    padding: "0 16px"
    typography: "{typography.prose}"
  badge-neutral:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    padding: "2px 6px"
    typography: "{typography.badge}"
  badge-warn:
    textColor: "{colors.warn-text}"
    rounded: "{rounded.sm}"
    padding: "2px 6px"
  badge-danger:
    textColor: "{colors.danger}"
    rounded: "{rounded.sm}"
    padding: "2px 6px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
    height: "36px"
  card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.card}"
    padding: "12px"
  approval-card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.card}"
    padding: "12px"
  user-row:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text}"
    rounded: "{rounded.card}"
    padding: "8px 12px"
  composer:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.card}"
  composer-send:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-text}"
    rounded: "{rounded.md}"
    size: "44px"
  nav-item:
    rounded: "{rounded.md}"
    padding: "6px 8px"
    typography: "{typography.body}"
  nav-item-active:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text}"
---

# Design System: Threavia

## Overview

**Creative North Star: "The Operations Log"**

Threavia is the category standard played straight: a clean working product at the craft level of Linear and Vercel, with no irony and no smuggled quirk. Its signature is structural, not ornamental. A session is an operations log that contains the conversation: every row hangs off one timestamp gutter, tool calls are one-line rows, the agent's prose runs unmarked beside an empty gutter, and the person's messages are tinted rows headed by the word "You". It is not a chat that happens to contain logs.

The surface is a neutral cool grey ground with white surfaces, 1px hairline borders and a single indigo accent for primary actions, focus and selection. Colour carries meaning only where something means something: green for done, amber for waiting, red for danger, each with a variant that stays legible as text. Depth is tonal; shadows are faint and reserved for things that float. The densest information on screen is the most important: the approval card puts the exact command in mono as its headline, with the machine, directory and risk underneath, and two large separated answers below.

Rejected by the build and the brief: the ChatGPT clone (pill composer, gradient send, chat bubbles on opposite sides), gradients outside the mark, glass and blur.

**Key Characteristics:**
- One left margin: a timestamp gutter down the whole session, every entry starting at the same edge.
- One accent (indigo), three state hues, everything else cool neutral.
- System UI type; monospace only for commands, paths, code and the gutter's times; tabular figures for every time, count, duration and cost.
- 6–8px radii, 1px hairlines, tonal depth, no gradients, no glass.
- Phone first where it matters: 44px targets for every decision, collapsing to 32–36px where the pointer is precise.
- Nothing fails silently: an outcome, a reason and a recovery, announced.

## Colors

A cool, near-colourless neutral system (hue 265, chroma under 0.01) with one saturated indigo and three state hues; both schemes are defined token for token and follow the operating system.

### Primary
- **Working Indigo** (`accent`, light `oklch(52% 0.19 275)`, dark `oklch(70% 0.15 275)`): the primary action (Approve on a non-dangerous request, Send, Answer), the focus ring, the selection tint, the caret, the active tab underline, links in agent prose and the "running" status. It is the only colour that means "act here".
- **Accent Ink** (`accent-text`): text and icons on an accent fill; near-white in light, near-black in dark because the dark accent is too light to carry white.
- **Mark Violet** (`accent-2`): the far end of the logo gradient. It belongs to the mark and nothing else.

### Secondary (state hues)
- **Done Green** (`ok`): completed jobs, the "live" badge, allowed receipts, additions in a diff.
- **Waiting Amber** (`warn`): fills, dots and icons that say something waits: the waiting badge tint, the rail dot, "Runs a command" risk tint.
- **Waiting Amber, Text Voice** (`warn-text`, light `oklch(49% 0.11 65)`): exists because amber as text on a light ground falls to about 2.5:1. Any amber *words* (the "N waiting" link, a waiting badge's label, "interrupted the job", a diff that could not load) use this darker voice. In dark the two are the same value, because light amber already reads on a dark ground.
- **Danger Red** (`danger`): danger as text and tint: failed tool calls, deletions in a diff, ActionError, the border and badge of a dangerous approval.
- **Danger Fill** (`danger-solid`, dark `oklch(50% 0.19 25)`): exists because the dark scheme's `danger` is a text colour, too light to carry white at 4.5:1. Every red button (Approve on a dangerous request, Delete, ConfirmAction's confirm) fills with `danger-solid` and white text, never with `danger`.

### Neutral
- **Canvas** (`canvas`): the page ground behind everything.
- **Surface** (`surface`): cards, the composer, the sidebar, dialogs, inputs.
- **Raised Grey** (`surface-2`): the second tone: secondary buttons, the person's tinted log rows, code blocks, hover fills, the active nav item.
- **Hairline** (`border`): every 1px border and divider, the scrollbar thumb.
- **Ink** (`text`): primary text.
- **Muted Ink** (`muted`, light `oklch(50% 0.01 265)`): secondary text, metadata, log lines, the gutter. Kept dark enough for 4.5:1 on white; it is never lightened further for readable text (`muted/70` appears only on the keyboard-hint line, which is desktop-only and redundant).

### Named Rules
**The One Accent Rule.** Indigo is the only colour that invites action. State hues describe; they never decorate.

**The Text-Voice Rule.** A state hue used as words uses its text-safe token: `warn-text` for amber words, `danger` for red words, `danger-solid` under white text. A fill token is never set as text, and a text token is never filled under white.

**The Mark-Only Gradient Rule.** The indigo-to-violet gradient lives in the logo. Nowhere else carries a gradient.

## Typography

**Body Font:** system-ui (with -apple-system, Segoe UI, sans-serif)
**Label/Mono Font:** ui-monospace (with SF Mono, JetBrains Mono, Menlo, Consolas, monospace)

**Character:** The operating system's own face, so the product feels native on every machine and phone; mono is a material that means "this is literal", not a style.

### Hierarchy
- **Headline** (600, 1.125rem): page titles such as "Waiting for you".
- **Title** (600, 0.9375rem): the session title in the header; dialog titles at 1rem.
- **Prose** (400, 0.9375rem, relaxed 1.625): the agent's markdown, bounded by the reading container (46rem). Prose headings step down to 1.125rem / 1rem / 0.875rem uppercase.
- **Body** (400, 0.875rem): the person's messages, nav, inputs, buttons, approval context.
- **Log line** (400, 0.8125rem, 20px line): plain events in the timeline, muted, with a 14px icon.
- **Meta** (400, 0.75rem): context strips, timestamps in cards, toolbar status, receipts' secondary text.
- **Label** (500, 0.75rem, uppercase, 0.025em tracking): form and sidebar section labels ("Project", "Sessions"). A field label, never an eyebrow above a headline.
- **Badge** (500, 0.6875rem): status and risk badges.
- **Gutter** (mono, 0.6875rem, 20px line, tabular): the HH:MM column of the log.

### Named Rules
**The Literal Mono Rule.** Monospace is reserved for what is literal: commands, paths, directories, code, tool names in a tool row, diff counts, costs and the gutter's clock. Prose, labels and headings never go mono.

**The Tabular Figures Rule.** Every time, duration, count and cost sets in tabular figures (`<time>` elements and the `figures` utility do it globally), so columns of numbers line up.

## Layout

A two-column app shell from 768px: the sidebar as a grid column (foldable to an icon rail) and the work area. Below 768px the sidebar becomes a left drawer (`min(20rem, 85vw)`) over a 40% black scrim, and a top bar carries the menu, the mark, the "N waiting" shortcut and the live badge.

Two content bounds: **reading** (46rem) for anything read top to bottom (the session log, the composer, the Waiting inbox) and **page** (64rem) for lists and forms that are scanned. A "wide layout" preference removes both bounds together, never one.

The log row is a two-column grid: a fixed gutter (2.75rem on the phone, 3.25rem from 640px) and the entry, 8px apart; rows breathe 6px vertically. The session stacks header, log, attention cards (capped at 45vh, scrollable) and composer; nothing else lives between the log and the hand.

Spacing is the 4px Tailwind rhythm: 8px between siblings, 12px inside cards, 16px page padding on the phone and 24px from 640px.

**The One Margin Rule.** Every entry in the log starts at the same left edge. The speaker is marked by a tinted row and a word, never by sending a message to the other side of the page or by a coloured side stripe.

## Elevation & Depth

Flat and tonal. Depth comes from the three-step ground (`canvas`, `surface`, `surface-2`) plus hairlines. Shadows exist only as a faint lift for things that sit over the log or float above the page; they are never decoration.

### Shadow Vocabulary
- **Lift** (`shadow-sm`): attention cards and the composer, which sit over the log; the selected segment in the delivery switch.
- **Float** (`shadow-md`): the round "jump to latest" button over the log.
- **Popover** (`shadow-lg`): select menus.
- **Overlay** (`shadow-xl`): dialogs and the mobile drawer, always with a 40% black scrim.

**The Tonal-First Rule.** If a surface can separate by tone and a hairline, it does. A shadow means "this is above the log", nothing else.

## Shapes

Gently rounded, never soft: 4px for small chips (badges, select items, checkboxes, segments), 6px for controls (buttons, inputs, nav items, code blocks), 8px for containers (cards, the composer, user rows, the approval card, dialogs). Full rounds are only for dots, avatars, the jump button and the floating day pill. Borders are always 1px hairlines; the dashed hairline is reserved for empty states.

**The Squared Composer Rule.** The composer is an 8px panel with a toolbar row, never a pill.

## Components

### Buttons
Restrained and exact; colour is earned by the action.
- **Shape:** 6px radius, medium weight, 16px icons, 8px gap.
- **Primary:** accent fill, accent ink. Hover drops to 90% opacity.
- **Secondary (default):** raised-grey fill with a hairline; hover darkens toward the border tone. Deny, choices and most commands.
- **Ghost:** muted text, no fill until hover. Toolbar Stop, icon controls, ConfirmAction's "No".
- **Danger:** `danger-solid` fill, white text. Approve on a dangerous request, Delete.
- **Link:** accent text, underline on hover.
- **Sizes:** `sm` 32px, `md` 36px (default), `icon` 32px square, and `lg`: **44px tall with 15px text on the phone**, collapsing to 36px/14px from 640px. Every decision (Deny, Approve, choices, Answer) uses `lg`; ad-hoc phone overrides (`min-h-11 sm:min-h-0`, `size-11 sm:size-8`) give the composer's Stop, Send and delivery segments the same 44px.
- **Focus / Disabled:** a 2px accent outline offset 2px, keyboard only (`:focus-visible`). Disabled is 50% opacity with no pointer events.

### Badges
- **Style:** 4px radius, 11px medium text, 2px × 6px padding, a 15% tint of the tone behind the tone's text voice: neutral (raised grey / muted), ok, warn (amber tint, `warn-text` words), danger, accent.
- **Use:** status ("live", "waiting validation"), risk labels, counts with tabular figures. A badge always carries a word, so state is never colour alone.

### Inputs / Fields
- **Style:** surface fill, 1px hairline, 6px radius, 36px tall (44px on the phone where it answers a question), muted placeholder, accent caret.
- **Focus:** the global accent focus ring; the composer instead shifts its border to accent with a 20% accent ring around the whole panel.
- **Labels:** the uppercase 12px Label above the field.

### Cards / Containers
- **Corner Style:** 8px.
- **Background:** surface, 1px hairline, 12px padding. Empty states use the same shape with a dashed hairline and muted text.
- **Shadow Strategy:** none at rest; `shadow-sm` only for attention cards and the composer (see Elevation).

### Navigation
- **Sidebar:** the mark and live badge, then **Waiting** first (inbox icon in `warn-text`, amber count badge, medium weight when something waits), the project select, project nav, then sessions grouped by "Waiting for you" (amber) and by recency. Items are 14px, 6px radius; hover and active take the raised-grey fill, active adds medium weight.
- **Rail:** folded, the sidebar keeps 32px icon squares; a waiting count becomes an amber dot ringed in surface.
- **Phone:** a top bar with the menu, the mark, an amber "N waiting" link that goes straight to the inbox, and the live badge. The drawer slides in over a scrim and is `inert` while closed, so it takes neither keyboard focus nor screen reader attention.
- **Tabs:** muted text, active text in ink with a 2px accent underline over a hairline.

### Session Log (signature)
- **Gutter:** a right-aligned mono 11px `HH:MM` column in muted ink, nudged to align with each row's first line. Agent prose leaves its gutter cell empty.
- **Rows:** the agent's markdown unmarked on the page; the person's message as a raised-grey row with a hairline and an 8px radius, headed "You" (or a clock icon and "Schedule"), with "interrupted the job" in `warn-text` when it cut in; tool calls as one-line rows (chevron, terminal icon, tool name in ink mono, the command truncated in muted mono, +/− counts in ok/danger), expanding into a bordered surface panel; other events as a 14px icon and a 13px muted line, with job cost in tabular mono.
- **Days:** a centered muted date between two hairlines; the current day floats as a full-round pill at the top while scrolling.

### Approval Card (signature)
The product's highest-stakes moment, read before it is answered.
1. **Head row:** the risk badge (neutral "Reads files", warn "Runs a command" / "Writes a file", danger "Pushes to a remote" / "Deletes or changes infrastructure"), the tool name in muted meta, the age on the right.
2. **Headline:** the exact command, path or URL in 14px mono, wrapped, never truncated; a file change shows its diff instead.
3. **Context strip:** machine (server icon), working directory (folder icon, mono), and in the Waiting inbox the project and session, all 12px muted.
4. **Full request:** a disclosure ("Full request") revealing the raw payload in a raised-grey mono block; its summary is 44px tall on the phone.
5. **Answers:** Deny (secondary) and Approve as `lg` buttons, a two-column grid on the phone with a 12px gutter between them, right-aligned from 640px. Approve turns `danger-solid` when the risk is danger, and the whole card takes a 50% danger border.
6. **After:** the card vanishes when Core resolves it; a receipt ("Allowed Bash: …, the agent continues" in ok, or "Denied …" in muted) stays for 4 seconds in a `role="status"` polite live region.

The question card shares the anatomy with an accent "Question" badge, a 14px medium prompt, `lg` choice buttons and a 44px answer field.

### Composer
- **Panel:** one 8px surface panel with a hairline and `shadow-sm`: an auto-growing borderless textarea, then a toolbar row.
- **Toolbar:** the delivery switch (a raised-grey segmented radiogroup: Queue / Next step / Interrupt, shortened to Queue / Next / Now on the phone; the selected segment lifts to surface with `shadow-sm`, Interrupt in `warn-text`), a spacer, the running status (desktop only), a ghost Stop with a filled square, and Send: a flat accent square with an up arrow, 44px on the phone and 32px from 640px.
- **Hint:** a centered desktop-only muted line, "Enter sends · Shift+Enter for a newline · Esc stops".

### ConfirmAction
A destructive action asks once, in place: the trigger is replaced on the spot by the question in muted 12px, then **No** (ghost, first) and the confirm verb (`danger-solid`), both `sm`. No modal and no second screen; one gesture learned for every permanent removal.

### ActionError
Every mutation says when it fails, next to the control that tried it, as `role="alert"` danger text in 14px: **outcome**, then Core's **reason**, then the **recovery**: "Not approved: connection lost. The request is still waiting; decide again once that is resolved." With no outcome given it reads as the capitalised reason followed by "Try again." The text that failed to send stays in its field.

## Do's and Don'ts

### Do:
- **Do** use `lg` buttons (44px on the phone) for every decision and give every other phone-reachable control a 44px target.
- **Do** put the literal thing being decided (command, path, URL) in mono as the headline, with machine, directory and risk beside it.
- **Do** use `warn-text` for amber words and `danger-solid` under white text, in both schemes.
- **Do** report every failed mutation with ActionError (outcome, reason, recovery) and every landed decision with a `role="status"` receipt.
- **Do** ask for permanent removals with ConfirmAction, in place, "No" first.
- **Do** keep every log entry on the gutter's single left margin and every time in tabular figures.
- **Do** pair every state colour with a word or icon; colour never carries state alone.

### Don't:
- **Don't** build the ChatGPT clone: no pill composer, no gradient send, no chat bubbles on opposite sides.
- **Don't** use a gradient outside the mark, or glass and backdrop blur anywhere.
- **Don't** introduce a second accent; state hues are not decoration.
- **Don't** set amber text in `warn` on a light ground (about 2.5:1), or white text on the dark scheme's `danger`.
- **Don't** set prose, labels or headings in monospace.
- **Don't** mark a speaker with a coloured side stripe; the mark is a tinted row and a word.
- **Don't** let a closed drawer keep focus: off-screen means `inert`.
