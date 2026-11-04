import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import type { KeyboardEvent, RefObject } from 'react';
import { File, Folder, LoaderCircle, Sparkles, Terminal } from 'lucide-react';

import { useBackendSkills, useSkills, useWorkspaceFiles } from '@/api/queries';
import { cn } from '@/lib/utils';
import {
  commandInsert,
  composerToken,
  matchCommands,
  replaceToken,
  type ComposerCommand,
  type ComposerToken,
} from '../../../../clients/shared/composer';

/**
 * The Skills a Job of this Session would have: the Project's own, the global
 * ones the Project did not turn off, and those that exist only on its backend.
 */
export function useSessionSkills(projectId: string, backendId: string | undefined) {
  const own = useSkills(projectId);
  const global = useSkills('');
  const local = useBackendSkills(backendId);
  return [
    ...(own.data ?? []),
    ...(global.data ?? []).filter((skill) => !(skill.disabledProjectIds ?? []).includes(projectId)),
    ...(local.data ?? []).filter((skill) => skill.available),
  ].map((skill) => ({ name: skill.name, description: skill.description }));
}

type Suggestion =
  | { kind: 'file'; id: string; path: string }
  | { kind: 'command'; id: string; command: ComposerCommand };

/**
 * What the field offers while someone types: `/` at the start of a message for
 * Skills and commands, `@` for a file of the working directory.
 *
 * The field stays the one place the keys go: the arrows move through the list,
 * Enter or Tab takes the line, Escape closes it — and only it, never also the
 * running work behind.
 */
export function useComposerSuggestions({
  field,
  message,
  setMessage,
  commands,
  files,
  onCommand,
}: {
  field: RefObject<HTMLTextAreaElement | null>;
  message: string;
  setMessage: (message: string) => void;
  commands: ComposerCommand[];
  /** The Session whose files @ names, when its backend can list them. */
  files?: { sessionId: string };
  /** A command that acts rather than writes; the field has already let go of it. */
  onCommand: (command: ComposerCommand) => void;
}) {
  const id = useId();
  const [caret, setCaret] = useState(0);
  const [highlighted, setHighlighted] = useState(0);
  // The token Escape closed stays closed until the caret leaves it.
  const [dismissed, setDismissed] = useState<number>();

  const found = composerToken(message, Math.min(caret, message.length));
  const token = found && found.start !== dismissed && (found.kind === 'command' || files) ? found : undefined;
  // Another word, another list: the first line is the one highlighted again.
  const tokenKey = token ? `${token.kind}:${token.start}:${token.query}` : '';
  const [seenKey, setSeenKey] = useState(tokenKey);
  if (seenKey !== tokenKey) {
    setSeenKey(tokenKey);
    setHighlighted(0);
  }

  // One request per pause in typing, not one per key.
  const [query, setQuery] = useState('');
  const wanted = token?.kind === 'file' ? token.query : undefined;
  useEffect(() => {
    if (wanted === undefined) return;
    const timer = window.setTimeout(() => setQuery(wanted), 120);
    return () => window.clearTimeout(timer);
  }, [wanted]);
  const listing = useWorkspaceFiles(files?.sessionId ?? '', query, Boolean(files) && wanted !== undefined);

  const suggestions: Suggestion[] = !token
    ? []
    : token.kind === 'command'
      ? matchCommands(commands, token.query).map((command) => ({ kind: 'command', id: command.id, command }))
      : (listing.data?.paths ?? []).map((path) => ({ kind: 'file', id: path, path }));
  const open = Boolean(token) && (suggestions.length > 0 || (token?.kind === 'file' && (listing.isFetching || Boolean(listing.error))));
  const active = Math.min(highlighted, Math.max(suggestions.length - 1, 0));

  // Where the caret goes once React wrote the new value into the field: in the
  // same commit, before the next key can land at the end of the field instead.
  const pendingCaret = useRef<number>(undefined);
  useLayoutEffect(() => {
    const position = pendingCaret.current;
    if (position === undefined || !field.current) return;
    pendingCaret.current = undefined;
    field.current.focus();
    field.current.setSelectionRange(position, position);
  });
  const placeCaret = (position: number) => {
    setCaret(position);
    pendingCaret.current = position;
  };

  const take = (suggestion: Suggestion, at: ComposerToken) => {
    setHighlighted(0);
    if (suggestion.kind === 'file') {
      const next = replaceToken(message, at, `@${suggestion.path}`);
      setMessage(next.text);
      placeCaret(next.caret);
      return;
    }
    const command = suggestion.command;
    const insert = commandInsert(command);
    const next = replaceToken(message, at, insert);
    setMessage(next.text);
    placeCaret(next.caret);
    if (!insert) onCommand(command);
  };

  /** Where the caret is, read after every change, click and key. */
  const track = () => {
    const element = field.current;
    if (!element) return;
    setCaret(element.selectionStart ?? element.value.length);
    const now = composerToken(element.value, element.selectionStart ?? element.value.length);
    if (dismissed !== undefined && now?.start !== dismissed) setDismissed(undefined);
  };

  /** True when the key belonged to the list and must go no further. */
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): boolean => {
    if (!open || !token || event.nativeEvent.isComposing) return false;
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        if (suggestions.length === 0) return false;
        event.preventDefault();
        const step = event.key === 'ArrowDown' ? 1 : -1;
        setHighlighted((active + step + suggestions.length) % suggestions.length);
        return true;
      }
      case 'Enter':
      case 'Tab': {
        if (suggestions.length === 0 || event.shiftKey) return false;
        event.preventDefault();
        take(suggestions[active], token);
        return true;
      }
      case 'Escape':
        event.preventDefault();
        event.stopPropagation();
        setDismissed(token.start);
        return true;
      default:
        return false;
    }
  };

  const list = open && token ? (
    <div
      id={id}
      role="listbox"
      aria-label={token.kind === 'command' ? 'Commands and skills' : 'Files'}
      className="bg-surface border-border absolute inset-x-0 bottom-full z-20 mb-1.5 max-h-72 overflow-y-auto rounded-lg border p-1 shadow-md"
    >
      {suggestions.map((suggestion, index) => (
        <div
          key={suggestion.id}
          id={`${id}-${index}`}
          role="option"
          aria-selected={index === active}
          // The field keeps the focus: a tap must not close the keyboard.
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => take(suggestion, token)}
          onMouseEnter={() => setHighlighted(index)}
          className={cn(
            'flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-2 text-sm sm:min-h-8',
            index === active ? 'bg-surface-2 text-text' : 'text-muted',
          )}
        >
          {suggestionLine(suggestion)}
        </div>
      ))}
      {token.kind === 'file' && suggestions.length === 0 && (
        <p className="text-muted flex items-center gap-2 px-2 py-1.5 text-xs" role="status">
          {listing.error ? `No files: ${listing.error instanceof Error ? listing.error.message : String(listing.error)}` : <><LoaderCircle className="size-3 animate-spin" /> Looking for files…</>}
        </p>
      )}
      {token.kind === 'file' && listing.data?.truncated && suggestions.length > 0 && (
        <p className="text-muted px-2 py-1 text-xs">More files match: keep typing to narrow them down.</p>
      )}
    </div>
  ) : null;

  return {
    list,
    onKeyDown,
    track,
    /** For the field: what it controls while the list is open. */
    fieldProps: open
      ? { 'aria-controls': id, 'aria-expanded': true, 'aria-autocomplete': 'list' as const, 'aria-activedescendant': suggestions.length ? `${id}-${active}` : undefined }
      : { 'aria-expanded': false },
  };
}

function suggestionLine(suggestion: Suggestion) {
  if (suggestion.kind === 'file') {
    const directory = suggestion.path.endsWith('/');
    const Icon = directory ? Folder : File;
    return (
      <>
        <Icon className="size-3.5 shrink-0" />
        <span className="min-w-0 truncate font-mono text-xs">{suggestion.path}</span>
      </>
    );
  }
  const command = suggestion.command;
  const Icon = command.kind === 'skill' ? Sparkles : Terminal;
  return (
    <>
      <Icon className="size-3.5 shrink-0" />
      <span className="shrink-0 font-mono text-xs">/{command.label}</span>
      {command.detail && <span className="text-muted min-w-0 truncate text-xs">{command.detail}</span>}
    </>
  );
}
