/**
 * What the message field offers while someone types: a `/` at the start of a
 * message for commands and Skills, an `@` anywhere for a file of the working
 * directory. Shared by the web client and the VS Code extension, so the same
 * keys do the same thing in both.
 */

/** The word being typed, when it asks for suggestions. */
export interface ComposerToken {
  kind: 'command' | 'file';
  /** What follows the `/` or the `@`. */
  query: string;
  /** Where the token starts, its `/` or `@` included, and where it ends. */
  start: number;
  end: number;
}

/**
 * The token under the caret, if it is one that offers suggestions.
 *
 * A command is the first word of the message: a `/` further on is a path or a
 * fraction, never a command. A file is any word that starts with `@`, once the
 * `@` follows a space or starts the message, so an e-mail address stays one.
 */
export function composerToken(text: string, caret: number): ComposerToken | undefined {
  let start = caret;
  while (start > 0 && !/\s/.test(text[start - 1])) start--;
  let end = caret;
  while (end < text.length && !/\s/.test(text[end])) end++;
  const word = text.slice(start, caret);
  if (word.startsWith('@')) {
    return { kind: 'file', query: word.slice(1), start, end };
  }
  if (word.startsWith('/') && text.slice(0, start).trim() === '') {
    return { kind: 'command', query: word.slice(1), start, end };
  }
  return undefined;
}

/** The field once a suggestion replaced the token, and where the caret goes. */
export function replaceToken(text: string, token: ComposerToken, replacement: string): { text: string; caret: number } {
  const before = text.slice(0, token.start);
  const after = text.slice(token.end);
  // A command that acted leaves nothing behind, not even its space.
  if (replacement === '') {
    return { text: before + after.replace(/^ /, ''), caret: before.length };
  }
  // One space after what was inserted, unless one is already there: the next
  // word follows without the person typing it.
  const spaced = /^\s/.test(after) ? replacement : `${replacement} `;
  return { text: before + spaced + after, caret: before.length + replacement.length + 1 };
}

/** How a provider is told, in the message itself, to use a Skill. */
export function skillMention(provider: string | undefined, name: string): string | undefined {
  switch (provider) {
    // Skills reach Claude Code as the session plugin threavia-skills, whose
    // Skills are its slash commands.
    case 'claude':
      return `/threavia-skills:${name}`;
    // Codex attaches the Skills named with a dollar sign.
    case 'codex':
      return `$${name}`;
    default:
      return undefined;
  }
}

/** One line of the `/` menu. */
export type ComposerCommand =
  | { kind: 'skill'; id: string; label: string; detail?: string; insert: string }
  | { kind: 'model'; id: string; label: string; detail?: string; model: string; effort: string }
  | { kind: 'effort'; id: string; label: string; detail?: string; effort: string }
  | { kind: 'delivery'; id: string; label: string; detail?: string; delivery: 'NEXT' | 'NOW' }
  | { kind: 'stop'; id: string; label: string; detail?: string };

export interface CommandSources {
  provider?: string;
  skills: { name: string; description?: string }[];
  /** The models of the backend, when the Session can change its model. */
  models?: { id: string; name: string; efforts: string[]; defaultEffort?: string }[];
  current?: { model?: string; effort?: string };
  /** How a message can reach the work running now. */
  deliveries?: ('NEXT' | 'NOW')[];
  /** Something is running that Stop would stop. */
  running?: boolean;
}

/**
 * Everything the `/` menu can offer, before filtering.
 *
 * Commands act on the Session and leave the message alone; a Skill is written
 * into the message, in the form its provider reads.
 */
export function composerCommands(sources: CommandSources): ComposerCommand[] {
  const commands: ComposerCommand[] = [];
  const seen = new Set<string>();
  for (const skill of sources.skills) {
    const insert = skillMention(sources.provider, skill.name);
    if (!insert || seen.has(skill.name)) continue;
    seen.add(skill.name);
    commands.push({ kind: 'skill', id: `skill:${skill.name}`, label: skill.name, detail: skill.description, insert });
  }
  const models = sources.models ?? [];
  const model = models.find((candidate) => candidate.id === sources.current?.model) ?? models[0];
  for (const candidate of models) {
    const effort = candidate.efforts.includes(sources.current?.effort ?? '') ? sources.current?.effort ?? '' : candidate.defaultEffort ?? candidate.efforts[0] ?? '';
    commands.push({ kind: 'model', id: `model:${candidate.id}`, label: `model ${candidate.id}`, detail: candidate.name === candidate.id ? undefined : candidate.name, model: candidate.id, effort });
  }
  for (const effort of model?.efforts ?? []) {
    commands.push({ kind: 'effort', id: `effort:${effort}`, label: `effort ${effort}`, effort });
  }
  if (sources.deliveries?.includes('NEXT')) {
    commands.push({ kind: 'delivery', id: 'next', label: 'next', detail: 'Send to the running job at its next step', delivery: 'NEXT' });
  }
  if (sources.deliveries?.includes('NOW')) {
    commands.push({ kind: 'delivery', id: 'now', label: 'now', detail: 'Interrupt the running job with this message', delivery: 'NOW' });
  }
  if (sources.running) {
    commands.push({ kind: 'stop', id: 'stop', label: 'stop', detail: 'Stop what is running now' });
  }
  return commands;
}

/** The commands that match what was typed after `/`, best first. */
export function matchCommands(commands: ComposerCommand[], query: string, limit = 12): ComposerCommand[] {
  const wanted = query.toLowerCase();
  if (!wanted) return commands.slice(0, limit);
  const scored: { command: ComposerCommand; score: number }[] = [];
  for (const command of commands) {
    const label = command.label.toLowerCase();
    // "model opus" is typed as "model opus" or as "opus"; a Skill by its name.
    const words = label.split(' ');
    const score = label.startsWith(wanted) ? 0
      : words.some((word) => word.startsWith(wanted)) ? 1
        : label.includes(wanted) ? 2
          : (command.detail ?? '').toLowerCase().includes(wanted) ? 3
            : -1;
    if (score >= 0) scored.push({ command, score });
  }
  return scored.sort((a, b) => a.score - b.score).slice(0, limit).map((entry) => entry.command);
}

/**
 * The messages a person wrote in a Session, newest first, without repeats: what
 * the up arrow brings back in an empty field.
 */
export function promptHistory(messages: { text: string; mine: boolean }[], limit = 50): string[] {
  const history: string[] = [];
  for (let index = messages.length - 1; index >= 0 && history.length < limit; index--) {
    const message = messages[index];
    const text = message.text.trim();
    if (!message.mine || !text || history.includes(text)) continue;
    history.push(text);
  }
  return history;
}

/**
 * Moving through the history with the arrows.
 *
 * `position` is -1 while the field holds what the person is writing, and the
 * index of the recalled message otherwise. Up starts only from an empty field,
 * so it never replaces a message being written; down past the newest gives the
 * empty field back.
 */
export function recall(history: string[], position: number, direction: 'older' | 'newer', current: string): { position: number; text: string } | undefined {
  if (direction === 'older') {
    if (position === -1 && current.trim() !== '') return undefined;
    if (position >= 0 && current !== history[position]) return undefined;
    const next = position + 1;
    return next < history.length ? { position: next, text: history[next] } : undefined;
  }
  if (position < 0 || current !== history[position]) return undefined;
  const next = position - 1;
  return { position: next, text: next < 0 ? '' : history[next] };
}

export interface ModelCatalog {
  defaultModel?: string;
  defaultEffort?: string;
  models: { id: string; name: string; efforts: string[]; defaultEffort?: string }[];
}

/**
 * What the model pill says: the model the next job uses — the backend's
 * default named as such — and its effort, chosen or by default.
 */
export function modelPill(catalog: ModelCatalog | undefined, model: string, effort: string): {
  name: string;
  effort?: string;
  unavailable: boolean;
  /** The efforts the shown model accepts; empty when it has none to choose. */
  efforts: string[];
} {
  const models = catalog?.models ?? [];
  const selected = models.find((option) => option.id === (model || catalog?.defaultModel));
  const unavailable = Boolean(model) && !selected;
  const fallback = catalog?.defaultModel ? models.find((option) => option.id === catalog.defaultModel)?.name ?? catalog.defaultModel : '';
  const name = model ? `${selected?.name ?? model}${unavailable ? ' (unavailable)' : ''}` : `Default${fallback ? ` · ${fallback}` : ''}`;
  const defaultEffort = model ? selected?.defaultEffort : catalog?.defaultEffort || selected?.defaultEffort;
  return { name, effort: effort || defaultEffort || undefined, unavailable, efforts: selected?.efforts ?? [] };
}
