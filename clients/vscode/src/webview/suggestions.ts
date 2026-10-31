import { composerToken, matchCommands, replaceToken, type ComposerCommand, type ComposerToken } from '../../../shared/composer';
import { h, icon } from './dom';

type Suggestion =
  | { kind: 'file'; id: string; path: string }
  | { kind: 'command'; id: string; command: ComposerCommand };

export interface SuggestionSources {
  commands: () => ComposerCommand[];
  /** Whether @ names files: the Session's backend can list them. */
  files: () => boolean;
  requestFiles: (query: string) => void;
  /** A command that acts rather than writes; the field has already let go of it. */
  onCommand: (command: ComposerCommand) => void;
  /** The field's value changed under the person's hands. */
  onChange: () => void;
}

/**
 * What the field offers while someone types: `/` at the start of a message
 * for Skills and commands, `@` for a file of the working directory. The same
 * rules as the web client, from the shared module; drawn here with the DOM.
 *
 * The field keeps the keys: the arrows move through the list, Enter or Tab
 * takes the line, Escape closes it — and only it, never the running work.
 */
export function createSuggestions(field: HTMLTextAreaElement, sources: SuggestionSources) {
  const element = h('div', { class: 'suggestions', role: 'listbox', id: 'composer-suggestions', hidden: true });
  let token: ComposerToken | undefined;
  let items: Suggestion[] = [];
  let highlighted = 0;
  let dismissed: number | undefined;
  let tokenKey = '';
  let files: { query: string; paths: string[]; truncated: boolean; error?: string } | undefined;
  let requested: string | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const open = () => !element.hidden;

  function update() {
    const found = composerToken(field.value, field.selectionStart ?? field.value.length);
    if (dismissed !== undefined && found?.start !== dismissed) dismissed = undefined;
    token = found && found.start !== dismissed && (found.kind === 'command' || sources.files()) ? found : undefined;
    const key = token ? `${token.kind}:${token.start}:${token.query}` : '';
    if (key !== tokenKey) {
      tokenKey = key;
      highlighted = 0;
    }
    if (token?.kind === 'file' && token.query !== requested) {
      const query = token.query;
      clearTimeout(timer);
      // One request per pause in typing, not one per key.
      timer = setTimeout(() => {
        requested = query;
        sources.requestFiles(query);
      }, 120);
    }
    render();
  }

  function render() {
    items = !token
      ? []
      : token.kind === 'command'
        ? matchCommands(sources.commands(), token.query).map((command) => ({ kind: 'command', id: command.id, command }))
        : (files?.paths ?? []).map((path) => ({ kind: 'file', id: path, path }));
    const waiting = token?.kind === 'file' && (requested !== token.query || files?.query !== token.query);
    const show = Boolean(token) && (items.length > 0 || (token?.kind === 'file' && (waiting || Boolean(files?.error))));
    element.hidden = !show;
    field.setAttribute('aria-expanded', String(show));
    if (!show || !token) {
      field.removeAttribute('aria-activedescendant');
      element.replaceChildren();
      return;
    }
    highlighted = Math.min(highlighted, Math.max(items.length - 1, 0));
    element.setAttribute('aria-label', token.kind === 'command' ? 'Commands and skills' : 'Files');
    const at = token;
    const rows = items.map((item, index) => {
      const row = h(
        'div',
        {
          id: `suggestion-${index}`,
          role: 'option',
          class: `suggestion${index === highlighted ? ' selected' : ''}`,
          'aria-selected': String(index === highlighted),
        },
        ...line(item),
      );
      // The field keeps the focus.
      row.addEventListener('mousedown', (event) => event.preventDefault());
      row.addEventListener('click', () => take(item, at));
      row.addEventListener('mouseenter', () => {
        highlighted = index;
        render();
      });
      return row;
    });
    const notes: HTMLElement[] = [];
    if (token.kind === 'file' && items.length === 0) {
      notes.push(h('p', { class: 'muted small suggestion-note', role: 'status' }, files?.error ? `No files: ${files.error}` : 'Looking for files…'));
    } else if (token.kind === 'file' && files?.truncated) {
      notes.push(h('p', { class: 'muted small suggestion-note' }, 'More files match: keep typing to narrow them down.'));
    }
    element.replaceChildren(...rows, ...notes);
    if (items.length) {
      field.setAttribute('aria-activedescendant', `suggestion-${highlighted}`);
      rows[highlighted]?.scrollIntoView({ block: 'nearest' });
    } else field.removeAttribute('aria-activedescendant');
  }

  function take(item: Suggestion, at: ComposerToken) {
    const replacement = item.kind === 'file' ? `@${item.path}` : item.command.kind === 'skill' ? item.command.insert : '';
    const next = replaceToken(field.value, at, replacement);
    field.value = next.text;
    field.focus();
    field.setSelectionRange(next.caret, next.caret);
    sources.onChange();
    if (item.kind === 'command' && item.command.kind !== 'skill') sources.onCommand(item.command);
    update();
  }

  /** True when the key belonged to the list and must go no further. */
  function onKeyDown(event: KeyboardEvent): boolean {
    if (!open() || !token || event.isComposing) return false;
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp':
        if (!items.length) return false;
        event.preventDefault();
        highlighted = (highlighted + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
        render();
        return true;
      case 'Enter':
      case 'Tab':
        if (!items.length || event.shiftKey) return false;
        event.preventDefault();
        take(items[highlighted], token);
        return true;
      case 'Escape':
        event.preventDefault();
        event.stopPropagation();
        dismissed = token.start;
        update();
        return true;
      default:
        return false;
    }
  }

  function receiveFiles(answer: { query: string; paths: string[]; truncated: boolean; error?: string }) {
    if (answer.query !== requested) return;
    files = answer;
    render();
  }

  field.setAttribute('aria-controls', 'composer-suggestions');
  field.setAttribute('aria-autocomplete', 'list');
  for (const name of ['input', 'click', 'keyup', 'select']) field.addEventListener(name, (event) => {
    // The keys the list answers do not move the caret.
    if (event instanceof KeyboardEvent && ['ArrowDown', 'ArrowUp', 'Enter', 'Tab', 'Escape'].includes(event.key)) return;
    update();
  });
  field.addEventListener('blur', () => {
    element.hidden = true;
    field.setAttribute('aria-expanded', 'false');
  });

  return { element, update, onKeyDown, receiveFiles };
}

function line(item: Suggestion): (Node | string)[] {
  if (item.kind === 'file') {
    return [icon(item.path.endsWith('/') ? 'folder' : 'file'), h('span', { class: 'suggestion-label' }, item.path)];
  }
  const command = item.command;
  return [
    icon(command.kind === 'skill' ? 'sparkle' : 'terminal'),
    h('span', { class: 'suggestion-label' }, `/${command.label}`),
    command.detail ? h('span', { class: 'muted suggestion-detail' }, command.detail) : '',
  ];
}
