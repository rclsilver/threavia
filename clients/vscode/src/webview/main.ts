import { payloadOf, type Delivery, type Event, type UserInputRequest, type ValidationRequest } from '../api/types';
import { artifactKind, bytes, clock, cost, duration, humanise, tokens } from '../conversation/format';
import type { DraftStart } from '../conversation/draft';
import type { HostMessage, PersistedState, SessionView, WebviewMessage } from '../conversation/protocol';
import { riskOf } from '../conversation/risk';
import {
  describe,
  fileOfTool,
  foldFinished,
  formatInput,
  rowsOf,
  stepsLabel,
  stickyPrompt,
  summariseInput,
  toolName,
  type Row,
} from '../conversation/timeline';
import { ago, headlineOf, toolLabel } from '../tree/model';
import { parsePathRef } from '../workspace/paths';
import { append, button, h, icon } from './dom';
import { Follower } from './follow';
import { renderMarkdown } from './markdown';

/**
 * The conversation, drawn in a webview.
 *
 * The extension sends the Session; this page draws it and says what the
 * person did. It looks like the editor because it is styled only with the
 * editor's theme variables, in whatever theme is on.
 */

interface VsCodeApi {
  postMessage(message: WebviewMessage): void;
  getState(): PersistedState | undefined;
  setState(state: PersistedState): void;
}
declare function acquireVsCodeApi(): VsCodeApi;

const vscode = acquireVsCodeApi();
const saved: PersistedState = vscode.getState() ?? { sessionId: document.body.dataset.sessionId ?? '' };
// Both change once, when a draft's first message creates its Session: the
// page stays, and what it keeps for a reload says which Session it shows.
let sessionId = saved.sessionId || (document.body.dataset.sessionId ?? '');
let start: DraftStart | undefined = sessionId ? undefined : saved.start;
// The Core never changes: it is kept so a panel restored after a reload
// opens the same Session on the same Core.
const coreId = saved.coreId || document.body.dataset.coreId || undefined;

const post = (message: WebviewMessage) => vscode.postMessage(message);

// ------------------------------------------------------------------- state

let events: Event[] = [];
let view: SessionView | undefined;
let rows: Row[] = [];
let loadingEarlier = false;
let delivery: Delivery = saved.delivery ?? 'QUEUE';
const opened = new Set<number>(saved.opened ?? []);
const expanded = new Set<string>(saved.expanded ?? []);
/** Which paths the agent wrote exist in the workspace, as the editor answered. */
const resolved = new Map<string, boolean>();
const asked = new Set<string>();
/**
 * Whether a draft's first message is on its way. Until Core answers there is
 * no Session to add to, and a second message must not start a second one.
 */
let starting = false;

function persist() {
  vscode.setState({
    coreId,
    sessionId,
    start,
    draft: field.value,
    delivery,
    opened: [...opened],
    expanded: [...expanded],
  });
}

// ---------------------------------------------------------------- skeleton

const title = h('h1', { class: 'title' });
const meta = h('div', { class: 'meta' });
const header = h('header', { class: 'header' }, title, meta);

const list = h('div', { class: 'timeline', role: 'list', 'aria-label': 'Conversation' });
const stickySlot = h('div', { class: 'sticky-slot' });
const earlierNote = h('div', { class: 'sticky-slot' });
const scroller = h('main', { class: 'scroller', tabindex: 0 }, stickySlot, earlierNote, list);
const toLatest = button(icon('arrow-down'), () => follower.toLatest(), {
  class: 'to-latest icon-button',
  title: 'Go to the latest',
  'aria-label': 'Go to the latest',
  hidden: true,
});
const timelineFrame = h('div', { class: 'timeline-frame' }, scroller, toLatest);
const attention = h('section', { class: 'attention', 'aria-label': 'Waiting for you' });
const receipts = h('div', { class: 'receipts', role: 'status', 'aria-live': 'polite' });

const field = h('textarea', { rows: 1, 'aria-label': 'Message', class: 'field' });
field.value = saved.draft ?? '';
const send = button(icon('send'), () => submit(), { class: 'send icon-button', title: 'Send (Enter)', 'aria-label': 'Send' });
const toolbar = h('div', { class: 'toolbar' });
const sendError = h('p', { class: 'error', role: 'alert', hidden: true });
const hint = h('p', { class: 'hint' });
const composer = h(
  'form',
  { class: 'composer' },
  h('div', { class: 'panel' }, h('div', { class: 'field-row' }, field, send), toolbar),
  sendError,
  hint,
);
composer.addEventListener('submit', (event) => {
  event.preventDefault();
  submit();
});

const bottom = h('div', { class: 'bottom' }, attention, receipts, composer);
document.body.append(header, timelineFrame, bottom);

const follower = new Follower(scroller, list, () => onMove());

// ---------------------------------------------------------------- messages

window.addEventListener('message', (message: MessageEvent<HostMessage>) => {
  const data = message.data;
  switch (data.type) {
    case 'session':
      view = data.view;
      renderHeader();
      renderAttention();
      renderComposer();
      renderTimeline();
      break;
    case 'draft':
      start = data.start;
      persist();
      renderHeader();
      renderComposer();
      renderTimeline();
      field.focus();
      break;
    case 'started':
      if (sessionId === data.sessionId) break;
      sessionId = data.sessionId;
      start = undefined;
      persist();
      renderTimeline();
      break;
    case 'events':
      if (data.reset) {
        events = data.events;
      } else {
        const known = new Set(events.map((event) => event.sequence));
        const fresh = data.events.filter((event) => !known.has(event.sequence));
        if (fresh.length === 0) break;
        const prepend = fresh.every((event) => event.sequence < (events[0]?.sequence ?? Infinity));
        events = [...events, ...fresh].sort((a, b) => a.sequence - b.sequence);
        if (prepend) {
          follower.keepPlace(() => renderTimeline());
          break;
        }
      }
      renderTimeline();
      break;
    case 'loadingEarlier':
      loadingEarlier = data.loading;
      earlierNote.replaceChildren(
        ...(loadingEarlier ? [h('p', { class: 'pill' }, 'Loading earlier messages…')] : []),
      );
      break;
    case 'paths':
      for (const [path, exists] of Object.entries(data.resolved)) resolved.set(path, exists);
      linkPaths(document.body);
      break;
    case 'sent':
      starting = false;
      delivery = 'QUEUE';
      sendError.hidden = true;
      send.disabled = !field.value.trim();
      renderComposer();
      persist();
      break;
    case 'sendFailed':
      starting = false;
      // The text goes back in the field, so nothing typed is lost.
      if (!field.value.trim()) field.value = data.text;
      sendError.textContent = `Not sent: ${data.error} Your message is still in the field; send it again.`;
      sendError.hidden = false;
      autosize();
      send.disabled = !field.value.trim();
      persist();
      break;
    case 'answered': {
      const done = awaiting.get(data.id);
      awaiting.delete(data.id);
      if (done && data.ok) receipt(done.text, done.approved);
      break;
    }
    case 'failed':
      list.replaceChildren(h('p', { class: 'empty error' }, data.message));
      break;
  }
});

// ------------------------------------------------------------------ header

function renderHeader() {
  if (!view && start) return renderDraftHeader(start);
  if (!view) return;
  title.textContent = view.title;
  const stateIcon = view.status.tone === 'running' ? 'loading' : view.status.tone === 'waiting' ? 'bell-dot' : 'circle-outline';
  meta.replaceChildren(
    h(
      'span',
      { class: `state tone-${view.status.tone}` },
      icon(stateIcon, view.status.tone === 'running' ? 'codicon-modifier-spin' : ''),
      view.status.label,
    ),
    ...(view.backend
      ? [h('span', { class: 'meta-item', title: 'The backend running this session' }, icon('server'), view.backend)]
      : []),
    ...(view.repository
      ? [h('span', { class: 'meta-item', title: view.repository.tooltip }, icon('git-branch'), view.repository.text)]
      : []),
    ...(view.active && view.activity ? [h('span', { class: 'activity' }, `${view.activity}…`)] : []),
  );
}

/** A draft's header: where the first message will go, chosen before it opened. */
function renderDraftHeader(draft: DraftStart) {
  title.textContent = 'New session';
  meta.replaceChildren(
    ...(draft.coreName
      ? [h('span', { class: 'meta-item', title: 'The Core the session will be created on' }, icon('cloud'), draft.coreName)]
      : []),
    h('span', { class: 'meta-item', title: 'The Project the session works in' }, icon('project'), draft.projectName),
    h('span', { class: 'meta-item', title: 'The backend that will run this session' }, icon('server'), draft.backendName),
    ...(draft.directoryName
      ? [h('span', { class: 'meta-item', title: 'The working directory the agent starts in' }, icon('folder'), draft.directoryName)]
      : []),
  );
}

// ---------------------------------------------------------------- timeline

const rowElements = new Map<string, { element: HTMLElement; signature: string }>();

function signature(row: Row): string {
  switch (row.kind) {
    case 'day':
      return `d${row.label}`;
    case 'steps':
      return JSON.stringify([row.steps, row.open]);
    case 'tool':
      return JSON.stringify([row.call, expanded.has(row.call.id)]);
    case 'event': {
      const job = row.event.jobId;
      const status = row.event.type === 'user.message' && job ? (view?.pending[job] ?? '') : '';
      return `${row.event.sequence}:${status}:${row.startedAt ?? ''}:${expanded.has(`files:${row.key}`)}`;
    }
  }
}

function renderTimeline() {
  rows = foldFinished(rowsOf(events), opened);
  const keep = new Set<string>();
  let previous: HTMLElement | null = null;
  for (const row of rows) {
    const key = String(row.key);
    keep.add(key);
    const sig = signature(row);
    let entry = rowElements.get(key);
    if (!entry || entry.signature !== sig) {
      const element = renderRow(row);
      entry?.element.replaceWith(element);
      entry = { element, signature: sig };
      rowElements.set(key, entry);
    }
    const expected: ChildNode | null = previous ? previous.nextSibling : list.firstChild;
    if (expected !== entry.element) list.insertBefore(entry.element, expected);
    previous = entry.element;
  }
  for (const [key, entry] of rowElements) {
    if (!keep.has(key)) {
      entry.element.remove();
      rowElements.delete(key);
    }
  }
  list.querySelector(':scope > .empty')?.remove();
  if (rows.length === 0 && view) list.append(h('p', { class: 'empty' }, 'Nothing yet.'));
  else if (rows.length === 0 && start) {
    list.append(h('p', { class: 'empty' }, 'Nothing is created until you send the first message.'));
  }
  linkPaths(list);
  follower.settle();
}

function renderRow(row: Row): HTMLElement {
  if (row.kind === 'day') {
    return h('div', { class: 'row day', role: 'listitem' }, h('span', { class: 'day-label' }, row.label));
  }
  const body =
    row.kind === 'steps' ? stepsEntry(row) : row.kind === 'tool' ? toolEntry(row) : eventEntry(row.event, row.startedAt);
  const agent = row.kind === 'event' && row.event.type === 'agent.message';
  const at = row.kind === 'tool' ? row.call.at : row.kind === 'steps' ? row.at : row.event.timestamp;
  // The log's spine: every row's time in one column, so the Session reads
  // down a timeline the way an operations log does. Agent prose keeps the
  // column empty and runs beside it.
  const gutter = agent ? h('span', { class: 'gutter' }) : h('time', { class: 'gutter', datetime: at }, clock(at));
  return h(
    'div',
    { class: `row ${row.kind}${row.kind === 'event' ? ` ${row.event.type.replace(/\./g, '-')}` : ''}`, role: 'listitem', 'data-key': String(row.key) },
    gutter,
    body,
  );
}

function stepsEntry(row: Extract<Row, { kind: 'steps' }>): HTMLElement {
  return button(
    [
      icon(row.open ? 'chevron-down' : 'chevron-right'),
      icon('layers'),
      h('span', {}, stepsLabel(row)),
      row.steps.failed > 0 && h('span', { class: 'failed' }, icon('warning'), `${row.steps.failed} failed`),
    ],
    () => {
      follower.holdStill();
      if (!opened.delete(row.key)) opened.add(row.key);
      persist();
      renderTimeline();
    },
    { class: 'steps-button', 'aria-expanded': String(row.open) },
  );
}

function toolEntry(row: Extract<Row, { kind: 'tool' }>): HTMLElement {
  const { call } = row;
  const open = expanded.has(call.id);
  const failed = Boolean(call.error);
  const summary = summariseInput(call.input).replace(/\s+/g, ' ');
  const status = !call.done
    ? icon('loading', 'codicon-modifier-spin tone-running')
    : failed
      ? icon('warning', 'tone-danger')
      : icon('terminal', 'muted');
  const head = button(
    [
      icon(open ? 'chevron-down' : 'chevron-right', 'muted'),
      status,
      h('span', { class: `tool-name${failed ? ' tone-danger' : ''}` }, toolName(call.name)),
      summary && h('span', { class: 'tool-summary' }, summary),
    ],
    () => {
      follower.holdStill();
      if (!expanded.delete(call.id)) expanded.add(call.id);
      persist();
      renderTimeline();
    },
    { class: 'tool-head', 'aria-expanded': String(open), title: failed ? 'Failed' : call.done ? undefined : 'Running' },
  );
  if (!open) return h('div', { class: 'tool' }, head);

  const file = fileOfTool(call.input);
  return h(
    'div',
    { class: 'tool open' },
    head,
    h(
      'div',
      { class: 'tool-body' },
      file && h('p', { class: 'tool-file' }, icon('go-to-file', 'muted'), h('code', { 'data-path': file }, file)),
      block('Input', formatInput(call.input)),
      call.error !== undefined && block('Error', call.error, 'danger'),
      call.output !== undefined && block('Output', call.output),
      !call.done && h('p', { class: 'muted small' }, 'Still running…'),
    ),
  );
}

function block(label: string, text: string, tone?: 'danger'): HTMLElement {
  const content = text.trim();
  return h(
    'div',
    { class: 'block' },
    h('span', { class: 'block-label' }, label),
    content
      ? h('pre', { class: `block-text${tone ? ` tone-${tone}` : ''}` }, content)
      : h('p', { class: 'muted small' }, 'empty'),
  );
}

function eventEntry(event: Event, startedAt?: string): HTMLElement {
  const user = payloadOf(event, 'user.message');
  if (user) {
    const status = event.jobId ? view?.pending[event.jobId] : undefined;
    return h(
      'div',
      { class: 'message-user' },
      h(
        'p',
        { class: 'message-meta' },
        user.scheduleId ? h('strong', {}, icon('calendar'), 'Schedule') : h('strong', {}, 'You'),
        // Sent while the agent worked: it changed the course of a Job rather
        // than starting one, and the reader of the log should know.
        user.delivery &&
          h(
            'span',
            { class: user.delivery === 'NOW' ? 'tone-warn' : undefined },
            user.delivery === 'NOW' ? 'interrupted the job' : 'added to the running job',
          ),
      ),
      h('p', { class: 'message-text' }, user.text),
      event.jobId && status && stopControl(event.jobId, status),
    );
  }

  const agent = payloadOf(event, 'agent.message');
  if (agent) {
    const prose = h('div', { class: 'prose' });
    // Rendered by markdown-it with raw HTML off: every character the agent
    // wrote is escaped or turned into markdown's own elements.
    prose.innerHTML = renderMarkdown(agent.text);
    enhanceCode(prose);
    return prose;
  }

  const changed = payloadOf(event, 'workspace.changed');
  if (changed) return workspaceChange(event, changed);

  const published = payloadOf(event, 'artifact.created');
  if (published) return artifactCard(published);

  const line = describe(event);
  return h(
    'p',
    { class: 'log' },
    icon(line.icon, line.tone ? `tone-${line.tone}` : 'muted'),
    h(
      'span',
      { class: line.tone === 'danger' && event.type === 'job.failed' ? 'tone-danger' : undefined },
      line.text,
      line.code && h('code', {}, line.code),
    ),
    jobCost(event, startedAt),
  );
}

/**
 * The control that stops one message, under the sentence it would stop, with
 * the status that makes it honest: dropping a message not started yet is not
 * the same act as interrupting one.
 */
function stopControl(jobId: string, status: string): HTMLElement {
  if (status === 'CANCELLING') return h('p', { class: 'stop muted small' }, 'Stopping…');
  const label = humanise(status);
  return h(
    'p',
    { class: 'stop' },
    h('span', { class: 'muted small' }, label.charAt(0).toUpperCase() + label.slice(1)),
    button([icon('debug-stop'), 'Stop'], () => post({ type: 'stop', jobId }), {
      class: 'ghost small',
      title: status === 'QUEUED' ? 'Drop this message before it runs' : 'Stop what this message started',
    }),
  );
}

/**
 * What a Job took and what it consumed. A Job whose accounting is unknown
 * must not read as a Job that cost nothing, so this shows nothing rather than
 * zeroes.
 */
function jobCost(event: Event, startedAt?: string): HTMLElement | false {
  const ended = payloadOf(event, 'job.completed') ?? payloadOf(event, 'job.failed');
  if (!ended) return false;
  const elapsed = startedAt ? Date.parse(event.timestamp) - Date.parse(startedAt) : NaN;
  const usage = ended.usage;
  const parts: HTMLElement[] = [];
  if (Number.isFinite(elapsed) && elapsed >= 0) parts.push(h('span', {}, duration(elapsed)));
  if (usage) {
    parts.push(
      h(
        'span',
        { title: `${usage.inputTokens} in, ${usage.outputTokens} out` },
        `${tokens(usage.inputTokens + usage.outputTokens)} tokens`,
      ),
    );
    if (usage.cacheReadTokens > 0) {
      parts.push(h('span', { title: 'Served from the prompt cache' }, `${tokens(usage.cacheReadTokens)} cached`));
    }
    if (usage.costUsd) parts.push(h('span', {}, cost(usage.costUsd)));
  }
  return parts.length > 0 && h('span', { class: 'cost' }, ...parts);
}

const MARK: Record<string, string> = { ADDED: '+', MODIFIED: '~', DELETED: '−', RENAMED: '→' };
const SHOWN_FILES = 12;

function workspaceChange(
  event: Event,
  change: NonNullable<ReturnType<typeof payloadOf<'workspace.changed'>>>,
): HTMLElement {
  const files = change.files ?? [];
  const all = expanded.has(`files:${event.sequence}`);
  const shown = all ? files : files.slice(0, SHOWN_FILES);
  // The backend can only be asked when it recorded the two trees.
  const diffable = Boolean(event.sessionId && change.baseTree && change.headTree);
  return h(
    'div',
    { class: 'card changes' },
    h(
      'p',
      { class: 'changes-head' },
      icon('diff', 'muted'),
      h('span', {}, `${files.length} file${files.length === 1 ? '' : 's'} changed`),
      h('span', { class: 'tone-added' }, `+${change.additions}`),
      h('span', { class: 'tone-deleted' }, `−${change.deletions}`),
    ),
    h(
      'ul',
      { class: 'files' },
      ...shown.map((file) => {
        const label = h('span', { class: `file state-${file.state.toLowerCase()}` }, `${MARK[file.state] ?? '~'} ${file.path}`);
        return h(
          'li',
          {},
          diffable
            ? button([label, icon('diff-single', 'file-action')], () => post({ type: 'openDiff', sequence: event.sequence, path: file.path }), {
                class: 'file-button',
                title: `Open the changes to ${file.path}`,
              })
            : label,
        );
      }),
      files.length > shown.length &&
        h(
          'li',
          {},
          button(`… and ${files.length - shown.length} more`, () => {
            follower.holdStill();
            expanded.add(`files:${event.sequence}`);
            persist();
            renderTimeline();
          }, { class: 'link-button' }),
        ),
    ),
  );
}

function artifactCard(artifact: NonNullable<ReturnType<typeof payloadOf<'artifact.created'>>>): HTMLElement {
  const kind = artifactKind(artifact.mimeType, artifact.filename);
  const kindIcon = kind === 'image' ? 'file-media' : kind === 'html' ? 'file-code' : kind === 'text' ? 'file-text' : 'file';
  const ref = { artifactId: artifact.artifactId, filename: artifact.filename, mimeType: artifact.mimeType, title: artifact.title };
  return h(
    'div',
    { class: 'card artifact' },
    icon(kindIcon, 'muted'),
    h(
      'div',
      { class: 'artifact-text' },
      h('p', { class: 'artifact-name' }, artifact.title || artifact.filename),
      h('p', { class: 'artifact-meta' }, `${artifact.title ? `${artifact.filename} · ` : ''}${bytes(artifact.size)}`),
    ),
    kind !== 'other' &&
      button([icon('open-preview'), 'Open'], () => post({ type: 'openArtifact', ...ref }), {
        class: 'ghost small',
        'aria-label': `Open ${artifact.filename}`,
      }),
    button([icon('save-as'), 'Save as…'], () => post({ type: 'saveArtifact', ...ref }), {
      class: 'ghost small',
      'aria-label': `Save ${artifact.filename} as…`,
    }),
  );
}

/** Gives each fenced block a copy button. */
function enhanceCode(root: HTMLElement) {
  for (const frame of root.querySelectorAll<HTMLElement>('.code-block')) {
    const code = frame.querySelector('code')?.textContent ?? '';
    const copy = button(icon('copy'), () => {
      void navigator.clipboard.writeText(code).then(() => {
        copy.replaceChildren(icon('check'));
        setTimeout(() => copy.replaceChildren(icon('copy')), 1500);
      });
    }, { class: 'copy icon-button', title: 'Copy', 'aria-label': 'Copy the code' });
    frame.append(copy);
  }
}

// ------------------------------------------------------------- file paths

/**
 * Marks what the agent wrote as a file — an inline code span, a relative
 * link, a tool's file — and asks the editor which of them exist in the
 * workspace. Only those become links: a path that opens nothing must not look
 * like it would.
 */
function linkPaths(root: HTMLElement) {
  const unknown: string[] = [];
  for (const element of root.querySelectorAll<HTMLElement>('.prose code:not(pre code), .prose a[href]')) {
    if (element.dataset.path !== undefined) continue;
    const text = element instanceof HTMLAnchorElement ? (element.getAttribute('href') ?? '') : (element.textContent ?? '');
    if (/^[a-z][a-z0-9+.-]*:/i.test(text) || !parsePathRef(text)) {
      element.dataset.path = '';
      continue;
    }
    element.dataset.path = text;
  }
  for (const element of root.querySelectorAll<HTMLElement>('[data-path]:not([data-path=""]):not(.path-link)')) {
    const path = element.dataset.path ?? '';
    const known = resolved.get(path);
    if (known) {
      element.classList.add('path-link');
      if (!(element instanceof HTMLAnchorElement)) {
        element.tabIndex = 0;
        element.setAttribute('role', 'link');
      }
      element.title = 'Open in the editor';
    } else if (known === undefined && !asked.has(path)) {
      asked.add(path);
      unknown.push(path);
    }
  }
  if (unknown.length > 0) post({ type: 'resolvePaths', paths: unknown });
}

document.addEventListener('click', (event) => {
  const target = event.target as Element;
  const path = target.closest<HTMLElement>('.path-link');
  if (path?.dataset.path) {
    event.preventDefault();
    post({ type: 'openPath', path: path.dataset.path });
    return;
  }
  const anchor = target.closest('a[href]');
  if (anchor) {
    // Nothing navigates the page itself: the editor decides what a link opens.
    event.preventDefault();
    const href = anchor.getAttribute('href') ?? '';
    if (/^(https?|mailto):/i.test(href)) post({ type: 'openLink', href });
    return;
  }
  focusComposer(event as MouseEvent);
});

document.addEventListener('keydown', (event) => {
  const target = event.target as HTMLElement;
  if ((event.key === 'Enter' || event.key === ' ') && target.classList.contains('path-link') && target.dataset.path) {
    event.preventDefault();
    post({ type: 'openPath', path: target.dataset.path });
  }
});

// ---------------------------------------------------------- sticky prompt

let promptKey: number | undefined;

/** After every move: the sticky prompt, the way back down, earlier history. */
function onMove() {
  toLatest.hidden = !follower.away;
  updateSticky();
  if (scroller.scrollTop < 400 && view?.more && !loadingEarlier) {
    loadingEarlier = true;
    post({ type: 'loadEarlier' });
  }
}

function updateSticky() {
  const geometry = rows.map((row) => {
    const element = rowElements.get(String(row.key))?.element;
    const top = element?.offsetTop ?? 0;
    return {
      user: row.kind === 'event' && row.event.type === 'user.message',
      top,
      bottom: top + (element?.offsetHeight ?? 0),
    };
  });
  const index = stickyPrompt(geometry, scroller.scrollTop, scroller.clientHeight);
  const row = index >= 0 ? rows[index] : undefined;
  const key = row?.key;
  if (key === promptKey) return;
  promptKey = key;
  if (!row || row.kind !== 'event') {
    stickySlot.replaceChildren();
    return;
  }
  const user = payloadOf(row.event, 'user.message');
  if (!user) return;
  const text = h('span', { class: 'sticky-text' }, user.text);
  const toggle = button(
    [h('span', { class: 'message-meta' }, h('strong', {}, user.scheduleId ? 'Schedule' : 'You'), clock(row.event.timestamp)), text],
    () => {
      const open = toggle.getAttribute('aria-expanded') !== 'true';
      toggle.setAttribute('aria-expanded', String(open));
      toggle.title = open ? 'Show less' : 'Show all of it';
    },
    { class: 'sticky-toggle', 'aria-expanded': 'false', title: 'Show all of it' },
  );
  stickySlot.replaceChildren(
    h(
      'div',
      { class: 'sticky-prompt' },
      h(
        'div',
        { class: 'sticky-card' },
        toggle,
        button(icon('arrow-up'), () => {
          follower.holdStill();
          rowElements.get(String(row.key))?.element.scrollIntoView({ block: 'start' });
        }, { class: 'icon-button', title: 'Go to this message', 'aria-label': 'Go to this message' }),
      ),
    ),
  );
}

// -------------------------------------------------------------- attention

const cards = new Map<string, HTMLElement>();

function renderAttention() {
  if (!view) return;
  const wanted = [
    ...view.validations.map((request) => ({ id: `v:${request.id}`, make: () => validationCard(request) })),
    ...view.questions.map((request) => ({ id: `q:${request.id}`, make: () => questionCard(request) })),
  ];
  const keep = new Set(wanted.map((card) => card.id));
  for (const [id, element] of cards) {
    if (!keep.has(id)) {
      element.remove();
      cards.delete(id);
    }
  }
  for (const card of wanted) {
    let element = cards.get(card.id);
    if (!element) {
      element = card.make();
      cards.set(card.id, element);
    }
    // Answered here and still shown: the answer did not land, so it can be
    // given again.
    for (const control of element.querySelectorAll<HTMLButtonElement>('button[data-answer]')) control.disabled = false;
    attention.append(element);
  }
  attention.hidden = wanted.length === 0;
}

function contextStrip(context: ValidationRequest['context']): HTMLElement | false {
  if (!context) return false;
  return h(
    'p',
    { class: 'context' },
    h('span', { title: 'The machine that would run it' }, icon('server'), context.backendName),
    context.directory && h('span', { title: "The session's working directory" }, icon('folder'), h('code', {}, context.directory)),
  );
}

function busy(card: HTMLElement) {
  for (const control of card.querySelectorAll<HTMLButtonElement>('button[data-answer]')) control.disabled = true;
}

/**
 * The card disappears the moment a decision lands, so what was decided is
 * said for a moment where it was — once the extension says it went through.
 */
const awaiting = new Map<string, { text: string; approved: boolean }>();

function receipt(text: string, approved: boolean) {
  const line = h('p', { class: approved ? 'tone-ok' : 'muted' }, icon(approved ? 'check' : 'close'), text);
  receipts.append(line);
  setTimeout(() => line.remove(), 4000);
}

/**
 * A permission request, the product's highest-stakes moment: what would run,
 * on which machine, in which directory, at what risk, read before it is
 * answered.
 */
function validationCard(request: ValidationRequest): HTMLElement {
  const payload = request.requestPayload as { tool?: unknown; input?: unknown };
  const tool = typeof payload.tool === 'string' ? payload.tool : '';
  const input = payload.input && typeof payload.input === 'object' ? (payload.input as Record<string, unknown>) : {};
  const risk = riskOf(tool, input);
  const headline = headlineOf(input);
  const decide = (approved: boolean) => {
    busy(card);
    awaiting.set(request.id, {
      text: `${approved ? 'Allowed' : 'Denied'} ${toolLabel(tool) || request.title}${
        headline ? `: ${headline.split('\n')[0]}` : ''
      }${approved ? ' — the agent continues' : ''}`,
      approved,
    });
    post({ type: 'decide', id: request.id, approved });
  };
  const card = h(
    'section',
    { class: `card request risk-${risk.tone}`, 'aria-label': `Approval needed: ${request.title}` },
    h(
      'div',
      { class: 'request-head' },
      h('span', { class: `badge tone-${risk.tone}` }, risk.label),
      tool && h('span', { class: 'muted' }, toolLabel(tool)),
      h('time', { class: 'muted push', datetime: request.createdAt }, ago(request.createdAt)),
    ),
    h('h2', { class: 'request-headline' }, headline || request.title),
    request.summary && !headline && h('p', { class: 'muted' }, request.summary),
    contextStrip(request.context),
    h(
      'details',
      { class: 'full-request' },
      h('summary', {}, 'Full request'),
      h('pre', { class: 'block-text' }, JSON.stringify(request.requestPayload, null, 2)),
    ),
    h(
      'div',
      { class: 'actions' },
      button('Deny', () => decide(false), { class: 'secondary', 'data-answer': true }),
      button('Approve', () => decide(true), { class: risk.tone === 'danger' ? 'danger' : 'primary', 'data-answer': true }),
    ),
  );
  return card;
}

function questionCard(request: UserInputRequest): HTMLElement {
  const choices = request.choices ?? [];
  const answer = (value: string) => {
    busy(card);
    awaiting.set(request.id, { text: `Answered: ${value}`, approved: true });
    post({ type: 'answer', id: request.id, value });
  };
  const input = h('input', { type: 'text', class: 'input', placeholder: 'Your answer', 'aria-label': 'Your answer' });
  const form = h(
    'form',
    { class: 'answer' },
    input,
    button('Answer', () => undefined, { type: 'submit', class: 'primary', 'data-answer': true }),
  );
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const value = input.value.trim();
    if (value) answer(value);
  });
  const card = h(
    'section',
    { class: 'card request', 'aria-label': 'Question from the agent', 'data-field-scope': true },
    h(
      'div',
      { class: 'request-head' },
      h('span', { class: 'badge tone-accent' }, 'Question'),
      h('time', { class: 'muted push', datetime: request.createdAt }, ago(request.createdAt)),
    ),
    h('h2', { class: 'request-prompt' }, request.prompt),
    contextStrip(request.context),
    choices.length > 0 &&
      h('div', { class: 'choices' }, ...choices.map((choice) => button(choice, () => answer(choice), { class: 'secondary', 'data-answer': true }))),
    (request.freeText || choices.length === 0) && form,
  );
  return card;
}

// --------------------------------------------------------------- composer

const PLACEHOLDERS: Record<Delivery | 'IDLE', string> = {
  IDLE: 'Send a message…',
  QUEUE: 'Queue a message for when this is done…',
  NEXT: 'Add something it reads at its next step…',
  NOW: 'Interrupt it and say what to do instead…',
};

const DELIVERY_LABELS: Record<Delivery, { label: string; title: string }> = {
  QUEUE: { label: 'Queue', title: 'Start a new job once this one is done' },
  NEXT: { label: 'Next step', title: 'Let the running job read it after what it is doing now' },
  NOW: { label: 'Interrupt', title: 'Stop what it is doing and redirect it with this message' },
};

/** The delivery in force: one the running work can take, or a queued message. */
function chosen(): Delivery {
  return view?.deliveries.includes(delivery as 'NEXT' | 'NOW') ? delivery : 'QUEUE';
}

function renderComposer() {
  const active = view?.active;
  field.placeholder = !view && start ? 'First message…' : PLACEHOLDERS[active ? chosen() : 'IDLE'];
  hint.textContent = `Enter sends · Shift+Enter for a newline${active ? ' · Esc stops' : ''}`;
  if (!active) {
    toolbar.hidden = true;
    toolbar.replaceChildren();
    return;
  }
  toolbar.hidden = false;
  const items: HTMLElement[] = [];
  if (active.status !== 'CANCELLING' && view && view.deliveries.length > 0) {
    const options: Delivery[] = ['QUEUE', ...view.deliveries];
    const group = h('div', { class: 'deliveries', role: 'radiogroup', 'aria-label': 'How to send' });
    for (const option of options) {
      const selected = chosen() === option;
      group.append(
        button(DELIVERY_LABELS[option].label, () => {
          delivery = option;
          persist();
          renderComposer();
          field.focus();
        }, {
          role: 'radio',
          'aria-checked': String(selected),
          title: DELIVERY_LABELS[option].title,
          class: `delivery${selected ? ' selected' : ''}${selected && option === 'NOW' ? ' tone-warn' : ''}`,
          tabindex: selected ? 0 : -1,
        }),
      );
    }
    // A radio group is one stop for Tab; the arrows move inside it.
    group.addEventListener('keydown', (event) => {
      if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) return;
      event.preventDefault();
      const step = event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1;
      const next = options[(options.indexOf(chosen()) + step + options.length) % options.length];
      delivery = next;
      persist();
      renderComposer();
      toolbar.querySelector<HTMLButtonElement>('.delivery.selected')?.focus();
    });
    items.push(group);
  }
  items.push(h('span', { class: 'push' }));
  if (active.status === 'CANCELLING') {
    items.push(h('span', { class: 'muted small' }, 'Stopping…'));
  } else {
    const label = humanise(active.status);
    items.push(
      h('span', { class: 'muted small' }, label.charAt(0).toUpperCase() + label.slice(1)),
      button([icon('debug-stop'), 'Stop'], () => stop(), { class: 'ghost small', title: 'Stop what is running now (Esc)' }),
    );
  }
  toolbar.replaceChildren(...items);
}

function stop() {
  const active = view?.active;
  if (active && active.status !== 'CANCELLING') post({ type: 'stop', jobId: active.id });
}

function submit() {
  const text = field.value.trim();
  if (!text || starting) return;
  starting = Boolean(start);
  post({ type: 'send', text, delivery: view?.active ? chosen() : 'QUEUE' });
  field.value = '';
  sendError.hidden = true;
  autosize();
  send.disabled = true;
  persist();
  follower.toLatest();
}

function autosize() {
  field.style.height = 'auto';
  field.style.height = `${Math.min(field.scrollHeight, 240)}px`;
}

field.addEventListener('input', () => {
  autosize();
  send.disabled = !field.value.trim();
  persist();
});
field.addEventListener('keydown', (event) => {
  // Enter sends, as in every chat; Shift+Enter is a newline. An Enter that
  // closes an input method is composing text, not sending it.
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    submit();
  }
});

// Escape stops what is running, as it interrupts Claude Code in a terminal —
// but not while it closes something else: an answer being typed, a menu.
window.addEventListener('keydown', (event) => {
  if (event.key !== 'Escape' || event.defaultPrevented || event.isComposing) return;
  if (event.target instanceof HTMLInputElement) return;
  stop();
});

/**
 * A click on the page with nothing under it means "back to writing": the
 * field takes the focus. Not on a control, not after selecting text to copy,
 * and a question's card keeps the click for its own field.
 */
function focusComposer(event: MouseEvent) {
  const target = event.target as Element;
  if (target.closest('a, button, input, textarea, select, label, summary, [role="link"], details')) return;
  if (window.getSelection()?.toString()) return;
  const own = target.closest('[data-field-scope]')?.querySelector<HTMLElement>('input, textarea');
  (own ?? field).focus({ preventScroll: true });
}

// ------------------------------------------------------------------ start

autosize();
send.disabled = !field.value.trim();
renderComposer();
// A draft restored after a reload is drawn from its own state at once; a
// Session waits for the extension to read it.
if (start) {
  renderHeader();
  renderTimeline();
} else {
  append(list, [h('p', { class: 'empty' }, 'Loading…')]);
}
post({ type: 'ready' });
