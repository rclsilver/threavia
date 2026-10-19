---
version: 1
slug: "web-ui-src-routes-review-tsx"
primary_target: "web/ui/src/routes/review.tsx"
related_targets: ["web/ui/src/components/review-code.tsx", "web/ui/src/components/review-draft.tsx", "web/ui/src/components/review-tree.tsx", "web/ui/src/components/review-notice.tsx", "web/ui/src/styles.css", "clients/vscode/src/review/view.ts"]
---

# Surface brief — human code review

Mode: Operate. This surface extends the incumbent Operations Log identity in
DESIGN.md. It introduces no visual world, palette, type family or graphic asset.
The approved direction is code-first: the owner reads actual changes, annotates
what needs correction, sends a draft batch, checks the resulting revision and
explicitly approves continuation. The Web interface is English. VS Code offers
the same review jobs through its native tree, diff editor and Comments panel.

## Direction contract

THESIS: Make human review a readable working editor whose code remains central
and whose continuation decision is explicit, including on a phone.

OWN-WORLD: Retain cool neutral canvas and surfaces, 1px hairlines, indigo action
and selection, meaningful green/amber/red states, system UI prose and literal
monospace code and paths. Use existing shared controls and tokens. The existing
mark, typography and light/dark system remain authoritative; DESIGN.md and
.impeccable/design.json are preserved. This surface establishes no new primitives.

STORY: Open the session's changes; identify repository and file; choose revision
and comparison; read diff or full before/after context; leave line, range, file
or whole-review drafts; send corrections together; inspect the new revision;
resolve or reopen discussions; approve the reviewed code explicitly. Approval
allows continuation and is distinct from merge or publication.

FIRST VIEWPORT: Header with conversation return, session title, review status
and Refresh; mandatory-waiting explanation when applicable; revision and
comparison selectors and open-discussion count; repository/file tree alongside
the code surface on desktop. The selected file heading carries repository,
path and Viewed, then diff/full-file controls. A footer exposes review comment,
draft count, Request corrections and Approve review. On mobile the Files button
opens an overlay file picker, review options are collapsible, and the diff stays
unified. Line selection is separate from the explicit Comment action.

FORM: Incumbent category standard; an editor composed from the existing neutral
surfaces, controls, hairlines and restrained state treatments. No illustration,
generated imagery, ornamental gradient or new identity is part of this surface.

FINISH: Source inspection is recorded below. Visual review remains recapture;
no current desktop or mobile screenshot exists. Local deployment validation is
required before commit or publication, and remains pending in this record.

## Evidence verified in source

- `web/ui/src/styles.css` retains the light/dark custom-property palette. Review
  additions use those tokens and color-mix rather than introducing palette
  literals. Additions have green tint and a plus sign; deletions have red tint
  and a minus sign. Waiting words use warn-text. Selection uses the accent.
- Desktop workspace is a 248px file column and minmax(0, 1fr) code column. The
  767px maximum-width rule hides the desktop file panel; Files opens a dialog
  over the unchanged code. `ReviewView` also suppresses split
  rendering at this breakpoint, independently of a previously selected split.
- Code is 0.8125rem with 1.65 line-height; paths and line gutters are monospace.
  Code wraps with preserved whitespace. Review body copy uses the incumbent
  system UI family; metadata is mostly 0.75rem and comments 0.875rem. Surface
  separation uses tonal fills, borders and 6px control / 8px thread corners.
- `ReviewTree` groups repository roots with name, directory, file count and
  declaration caveat, then native details/summary folders. Files carry a
  textual change initial, selected aria-current and a Viewed icon when the
  saved tree identity matches. These observations do not prove screen-reader
  behavior in a running browser.
- `ReviewCode` renders commentable before/after gutters, unified or paired diff
  rows, and full-side context. Shift-click extends a same-side range; Range
  chooses an end line on touch. Only Comment opens the editor dialog, with
  source excerpt and explicit start/end fields. Binary and
  oversized previews explain why line comments are unavailable and direct the
  owner to file comments. Alternate comparison baselines disable before-side
  selection and explain the original-line-number constraint.
- `ReviewDraft` saves nonempty changed text after an 800ms debounce, shows
  Saving / not saved / saved states through role=status, and exposes ActionError
  plus Retry on failure. Navigation and review actions call its flush method;
  rejected saves retain the field. This is source evidence, not an executed
  cross-device persistence check.
- `ReviewView` presents line/range, file and whole-review entry points; private
  draft edit/discard actions; batched Request corrections; revision selection;
  old-discussion location/excerpt; and Resolve/Reopen actions. It labels users,
  agents, drafts and resolved state with words. Mandatory review and correcting
  states explicitly describe the continuation gate. Approval is visibly
  disabled for old revisions, unresolved discussions, saved drafts, repository
  errors and an active correction turn; the backend's enforcement is outside
  this design inspection.
- Action errors use the shared ActionError component with outcome and recovery;
  receipts and loading states use role=status. Empty, unavailable, binary and
  preview-limit states have explicit copy. Focus uses the global keyboard-only
  2px accent outline. Runtime focus movement and announcements remain untested.
- `clients/vscode/src/review/view.ts` uses TreeDataProvider, ThemeIcon,
  vscode.diff, CommentController and native source ranges. Revision/comparison
  choices use Quick Pick. Comments are saved explicitly as drafts; native
  thread state exposes resolve/reopen. This confirms native presentation in
  source, not an installed-extension validation or complete behavioral parity.

## Validation states and unresolved limits

**Source inspection:** completed for PRODUCT.md, DESIGN.md, the existing sidecar,
review route, review components, global/review styles, shared Button and native
VS Code review view. This records observed implementation rather than a new
normative design system.

**Automated pattern detection:** the parent workflow reported `[]` for this
interface. That narrow result supplies no rendered visual or interaction proof.

**Finish review:** recapture for desktop and mobile, as reported by the parent
workflow. No screenshot was available to this pass; no visual ship verdict is
claimed. The user's first Android trial reported a black page on HTTP; its
secure-context UUID crash was reproduced and fixed. A later trial found Android
navigation difficult and desktop comment focus/scroll jumps disruptive. These
are failed validation results, not approval. Source follow-up separates gutter
selection from explicitly opening a comment dialog, preserves reading position,
uses a modal Files picker, moves previous/next to the file heading and folds
review options on mobile. DOM tests cover selection, dialog focus and scroll
restoration. Actual device comfort and native VS Code still need confirmation.

**Local/user validation:** the user explicitly authorized merging on
2026-10-19, after the local deployment and the usability fixes above. This
authorization does not constitute a separate screenshot-based finish verdict
or a claim that native VS Code was visually inspected. Follow docs/reviews.md
for the desktop, Android and native VS Code walkthrough. Release and production
deployment remain separate decisions.

**Follow-up source correction:** after documentation, the parent added a review
mobile override giving buttons and range-number fields a minimum height of 44px
through 767px. Rendered touch suitability remains unverified. The existing
sidecar's 2026-10-08 capture was preserved; updating any pre-existing drift was
outside scope.
