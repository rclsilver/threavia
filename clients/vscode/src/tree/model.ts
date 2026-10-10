import type { Project, Repository, Session, UserInputRequest, ValidationRequest } from '../api/types';

/**
 * What the sidebar shows, decided without an editor.
 *
 * The rules are the web sidebar's (web/ui/src/components/app-shell.tsx): what
 * waits for the user comes first, a permission outranks a question, archived
 * Sessions stay out of the list, and a Session with nothing going on is quiet.
 * The tree provider only turns these into TreeItems.
 */

export interface Pending {
  validations: ValidationRequest[];
  userInputs: UserInputRequest[];
}

export type SessionActivity = 'validation' | 'input' | 'running' | 'stopping' | 'queued' | 'idle';

/** What waits for the user, by Session. A permission outranks a question. */
export function waitingBySession(pending: Pending): Map<string, 'validation' | 'input'> {
  const waiting = new Map<string, 'validation' | 'input'>();
  for (const request of pending.userInputs) waiting.set(request.scope.sessionId, 'input');
  // Set last, so it overwrites: it is the one blocking a tool call.
  for (const request of pending.validations) waiting.set(request.scope.sessionId, 'validation');
  return waiting;
}

/** Requests, not sessions: one Session may ask twice, and each needs an answer. */
export function waitingCount(pending: Pending): number {
  return pending.validations.length + pending.userInputs.length;
}

export function sessionState(session: Session, waiting: Map<string, 'validation' | 'input'>): SessionActivity {
  const asked = waiting.get(session.id);
  if (asked) return asked;
  switch (session.activeJobStatus) {
    case undefined:
      return 'idle';
    case 'QUEUED':
      return 'queued';
    case 'CANCELLING':
      return 'stopping';
    default:
      return 'running';
  }
}

/** A ThemeIcon id and colour, as data so the choice is tested. */
export interface IconSpec {
  id: string;
  color?: string;
  label: string;
}

/**
 * The two states that wait are the loud ones, in the warning colour: they are
 * the only ones a person has to act on. Working spins; idle is a plain bubble,
 * which is most of the list.
 */
export function stateIcon(state: SessionActivity): IconSpec {
  switch (state) {
    case 'validation':
      return { id: 'shield', color: 'list.warningForeground', label: 'Waiting for your approval' };
    case 'input':
      return { id: 'question', color: 'list.warningForeground', label: 'Waiting for your answer' };
    case 'running':
      return { id: 'loading~spin', label: 'Working' };
    case 'stopping':
      return { id: 'loading~spin', color: 'disabledForeground', label: 'Stopping' };
    case 'queued':
      return { id: 'clock', label: 'Queued' };
    case 'idle':
      return { id: 'comment-discussion', label: 'Idle' };
  }
}

/**
 * The Sessions of a Project as the sidebar lists them: archived ones left out,
 * what waits first whatever its age, then the most recently touched.
 */
export function orderSessions(sessions: Session[], waiting: Map<string, 'validation' | 'input'>): Session[] {
  const recent = (session: Session) => {
    const at = Date.parse(session.updatedAt);
    // An unreadable date sorts last, which beats disappearing from the list.
    return Number.isNaN(at) ? -Infinity : at;
  };
  return sessions
    .filter((session) => session.status !== 'ARCHIVED')
    .sort((a, b) => {
      const waitingA = waiting.has(a.id) ? 0 : 1;
      const waitingB = waiting.has(b.id) ? 0 : 1;
      return waitingA - waitingB || recent(b) - recent(a);
    });
}

/** Whether a Project is the one this workspace names, by id or by name. */
export function isCurrentProject(project: Project, setting: string): boolean {
  const wanted = setting.trim();
  if (!wanted) return false;
  return project.id === wanted || project.name.toLowerCase() === wanted.toLowerCase();
}

/** The workspace's Project first, the others in Core's order; archived ones out. */
export function orderProjects(projects: Project[], setting: string): Project[] {
  const active = projects.filter((project) => project.status !== 'ARCHIVED');
  const current = active.filter((project) => isCurrentProject(project, setting));
  return [...current, ...active.filter((project) => !current.includes(project))];
}

export const UNTITLED = 'Untitled session';

export function titleOf(session: Pick<Session, 'title'>): string {
  return session.title || UNTITLED;
}

/**
 * A pinned Session from another Project names it, since the title alone does
 * not say where a click lands. One from the workspace's Project does not.
 */
export function pinnedDescription(session: Session, projects: Project[], setting: string): string | undefined {
  const project = projects.find((candidate) => candidate.id === session.projectId);
  if (!project || isCurrentProject(project, setting)) return undefined;
  return project.name;
}

// -------------------------------------------------------------------- Cores

/** Where the extension stands with one Core. */
export type CoreState = 'connecting' | 'unreachable' | 'signedOut' | 'ready';

/**
 * Whether the sidebar has a row per Core. With one Core it does not: the tree
 * is the one it always was, and a person who never adds a second Core never
 * sees the extra level.
 */
export function showsCores(count: number): boolean {
  return count > 1;
}

/**
 * What the top of the sidebar is. Nothing with no Core, or with one that
 * cannot be asked, so the view's welcome says what to do; one Core's sections
 * directly; a row per Core when there are several, each saying for itself
 * whether it can be asked.
 */
export function sidebarRoots(
  cores: readonly { id: string; state: CoreState }[],
): { kind: 'empty' } | { kind: 'flat'; coreId: string } | { kind: 'cores'; coreIds: string[] } {
  if (showsCores(cores.length)) return { kind: 'cores', coreIds: cores.map((core) => core.id) };
  const only = cores[0];
  return only?.state === 'ready' ? { kind: 'flat', coreId: only.id } : { kind: 'empty' };
}

/** One Core's part of the sidebar: what waits, what is pinned, its Projects. */
export type Section = { type: 'waiting' } | { type: 'pinned' } | { type: 'project'; project: Project };

export function coreSections(pinned: number, projects: Project[], setting: string): Section[] {
  return [
    { type: 'waiting' },
    // Nothing at all until something is pinned, so a person who never pins
    // sees the sidebar they had.
    ...(pinned > 0 ? [{ type: 'pinned' } as const] : []),
    ...orderProjects(projects, setting).map((project): Section => ({ type: 'project', project })),
  ];
}

/** The icon of a Core's row: whether it is connected, on its way, or needs the person. */
export function coreIcon(state: CoreState): IconSpec {
  switch (state) {
    case 'ready':
      return { id: 'vm-active', color: 'testing.iconPassed', label: 'Connected' };
    case 'connecting':
      return { id: 'loading~spin', label: 'Connecting' };
    case 'signedOut':
      return { id: 'account', color: 'list.warningForeground', label: 'Signed out' };
    case 'unreachable':
      return { id: 'error', color: 'errorForeground', label: 'Does not answer' };
  }
}

/** What a Core's row says under it while it cannot show its sessions. */
export function coreMessage(state: CoreState): { text: string; icon?: string; command?: 'signIn' | 'refresh' } | undefined {
  switch (state) {
    case 'ready':
      return undefined;
    case 'connecting':
      return { text: 'Connecting…' };
    case 'signedOut':
      return { text: 'Sign in to see its sessions.', icon: 'account', command: 'signIn' };
    case 'unreachable':
      return { text: 'It does not answer. Click to try again.', icon: 'error', command: 'refresh' };
  }
}

/**
 * Where the extension stands overall, for the welcome views and the commands
 * that need a Core: ready as soon as one Core is, since every command then
 * has somewhere to go.
 */
export function overallState(states: readonly CoreState[]): CoreState | 'unconfigured' {
  if (states.length === 0) return 'unconfigured';
  for (const state of ['ready', 'connecting', 'signedOut', 'unreachable'] as const) {
    if (states.includes(state)) return state;
  }
  return 'unreachable';
}

/** One Core's share of what waits. */
export interface CoreWaiting {
  name: string;
  count: number;
}

/**
 * The status bar's count, the sum over every Core, and its tooltip, which
 * says where they wait when there is more than one Core to wait in.
 */
export function waitingSummary(cores: readonly CoreWaiting[]): { count: number; tooltip: string } {
  const count = cores.reduce((sum, core) => sum + core.count, 0);
  if (cores.length <= 1) return { count, tooltip: `${count} waiting for you in Threavia` };
  const lines = cores.filter((core) => core.count > 0).map((core) => `${core.name}: ${core.count}`);
  return { count, tooltip: [`${count} waiting for you in Threavia`, ...lines].join('\n') };
}

/** A notification names its Core only when it could have come from another. */
export function withCore(text: string, coreName: string, several: boolean): string {
  return several ? `${coreName} · ${text}` : text;
}

// ------------------------------------------------------------------ waiting

/** One row of the Waiting node: a request and what it asks. */
export interface WaitingItem {
  kind: 'validation' | 'input';
  id: string;
  sessionId: string;
  /** What it asks, short enough for a row. */
  label: string;
  /** Which Session asks it. */
  description: string;
  /** Everything a person deciding it needs: the full ask, where it comes from. */
  detail: string;
  createdAt: string;
}

/** The field that says what a call actually does: a command, a path, a URL. */
const TELLING = ['command', 'file_path', 'path', 'notebook_path', 'url', 'pattern', 'query', 'prompt'];

export function headlineOf(input: Record<string, unknown>): string {
  for (const key of TELLING) {
    const value = input[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
  }
  return '';
}

/** Strips the mcp__<server>__ prefix, which is noise to a person. */
export function toolLabel(name: string): string {
  return name.replace(/^mcp__threavia__/, '').replace(/^mcp__([^_]+)__/, '$1: ');
}

/** The tool and its telling argument, read from a validation's payload. */
export function validationAsk(request: ValidationRequest): { tool: string; headline: string } {
  const payload = request.requestPayload as { tool?: unknown; input?: unknown };
  const tool = typeof payload.tool === 'string' ? toolLabel(payload.tool) : '';
  const input =
    payload.input && typeof payload.input === 'object' ? (payload.input as Record<string, unknown>) : {};
  return { tool, headline: headlineOf(input) };
}

/** One line, cut where a row would cut it anyway. */
export function oneLine(text: string, max = 120): string {
  const line = text.split('\n')[0].trim();
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

function where(request: ValidationRequest | UserInputRequest): string {
  const context = request.context;
  if (!context) return '';
  const parts = [`${context.projectName} · ${context.sessionTitle || UNTITLED}`, `on ${context.backendName}`];
  if (context.directory) parts.push(`in ${context.directory}`);
  return parts.join('\n');
}

/** What waits, oldest first: the first asked has waited longest. */
export function waitingItems(pending: Pending): WaitingItem[] {
  const validations = pending.validations.map((request): WaitingItem => {
    const { tool, headline } = validationAsk(request);
    const ask = headline ? `${tool ? `${tool}: ` : ''}${headline}` : request.title;
    return {
      kind: 'validation',
      id: request.id,
      sessionId: request.scope.sessionId,
      label: oneLine(ask),
      description: request.context?.sessionTitle || UNTITLED,
      detail: [ask, request.summary && request.summary !== ask ? request.summary : '', where(request)]
        .filter(Boolean)
        .join('\n\n'),
      createdAt: request.createdAt,
    };
  });
  const inputs = pending.userInputs.map(
    (request): WaitingItem => ({
      kind: 'input',
      id: request.id,
      sessionId: request.scope.sessionId,
      label: oneLine(request.prompt),
      description: request.context?.sessionTitle || UNTITLED,
      detail: [
        request.prompt,
        request.choices?.length ? `Choices: ${request.choices.join(', ')}` : '',
        where(request),
      ]
        .filter(Boolean)
        .join('\n\n'),
      createdAt: request.createdAt,
    }),
  );
  return [...validations, ...inputs].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
}

// --------------------------------------------------------------- repository

/** The short form, for a row's description: `main ↑1 ↓2 ●`. */
export function repositoryDescription(repo: Repository): string | undefined {
  if (!repo.tracked) return undefined;
  const parts = [repo.branch || repo.head || 'no commit'];
  if (repo.upstream && !repo.upstreamGone) {
    if (repo.ahead > 0) parts.push(`↑${repo.ahead}`);
    if (repo.behind > 0) parts.push(`↓${repo.behind}`);
  }
  if (changesOf(repo) > 0) parts.push(repo.conflicted > 0 ? '⚠' : '●');
  return parts.join(' ');
}

function changesOf(repo: Repository): number {
  return repo.staged + repo.unstaged + repo.untracked + repo.conflicted;
}

/** The long form, in words, for a tooltip. */
export function repositoryLines(repo: Repository, now: number = Date.now()): string[] {
  if (!repo.tracked) return ['Not a git repository'];
  const lines = [repo.branch ? `Branch ${repo.branch}` : `Detached at ${repo.head ?? 'no commit'}`];

  if (!repo.upstream) lines.push('Tracks nothing');
  else if (repo.upstreamGone) lines.push(`${repo.upstream} no longer exists`);
  else if (repo.ahead === 0 && repo.behind === 0) lines.push(`Level with ${repo.upstream}`);
  else {
    const moves = [repo.ahead > 0 && `${repo.ahead} to push`, repo.behind > 0 && `${repo.behind} to pull`];
    lines.push(`${moves.filter(Boolean).join(', ')} (${repo.upstream})`);
  }

  const changes = [
    repo.conflicted > 0 && `${repo.conflicted} in conflict`,
    repo.staged > 0 && `${repo.staged} staged`,
    repo.unstaged > 0 && `${repo.unstaged} modified`,
    repo.untracked > 0 && `${repo.untracked} untracked`,
  ].filter(Boolean);
  lines.push(changes.length ? changes.join(' · ') : 'Nothing to commit');

  lines.push(repo.fetchedAt ? `Fetched ${ago(repo.fetchedAt, now)}` : 'Never fetched');
  if (repo.fetchError) lines.push(`Fetch failed: ${repo.fetchError}`);
  lines.push(repo.directory);
  return lines;
}

/** A coarse, readable age: "just now", "5 min ago", "3 h ago", "2 d ago". */
export function ago(iso: string, now: number = Date.now()): string {
  const seconds = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (Number.isNaN(seconds)) return 'at an unknown time';
  if (seconds < 60) return 'just now';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}
