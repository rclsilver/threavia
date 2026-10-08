import { ChevronDown, ChevronRight, MoreHorizontal, Plus, type LucideIcon } from 'lucide-react';
import { useState, type ReactNode } from 'react';

import { Button } from '@/components/ui/button';
import { Menu, MenuContent, MenuTrigger } from '@/components/ui/menu';
import { cn } from '@/lib/utils';

/**
 * The lists of what a Project holds — decisions, files, skills — drawn one way.
 *
 * A row is a mark, a name, a line or two about it and when, with the one move
 * that usually comes next and everything else behind a menu. They used to be a
 * card each with their actions spelled out as lowercase links: a stack of
 * boxes, and a column of coloured words read before any name.
 */

/** One bordered list of rows, separated by hairlines rather than boxed. */
export function RecordList({ children }: { children: ReactNode }) {
  return <ul className="border-border divide-border divide-y rounded-(--radius-card) border">{children}</ul>;
}

export function RecordRow({
  icon: Icon,
  mark,
  tone = 'text-muted',
  title,
  badges,
  body,
  meta,
  action,
  menu,
  menuLabel,
  below,
  muted = false,
}: {
  icon: LucideIcon;
  /** A mark that is also a control, drawn in the icon's place. */
  mark?: ReactNode;
  tone?: string;
  title: ReactNode;
  badges?: ReactNode;
  body?: string;
  meta?: ReactNode;
  /** The one move a row offers outright. */
  action?: ReactNode;
  /** Everything else, as menu items. */
  menu?: ReactNode;
  menuLabel: string;
  /** A line under the row that takes its whole width: a question, an error. */
  below?: ReactNode;
  muted?: boolean;
}) {
  const [unfolded, setUnfolded] = useState(false);
  return (
    <li className="flex flex-wrap items-start gap-2 px-2 py-2 sm:gap-3 sm:px-3">
      {mark ?? (
        <span className="flex size-11 shrink-0 items-center justify-center sm:size-8">
          <Icon className={cn('size-4', tone)} />
        </span>
      )}
      <div className="min-w-0 flex-1 py-0.5 sm:py-1">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className={cn('min-w-0 text-sm font-medium break-words', muted && 'text-muted')}>{title}</span>
          {badges}
        </p>
        {body && (
          // A few lines in the list; the rest on a click, for whoever wants it.
          <button
            type="button"
            onClick={() => setUnfolded(!unfolded)}
            aria-expanded={unfolded}
            className={cn(
              'text-muted mt-0.5 block w-full text-left text-sm whitespace-pre-wrap',
              !unfolded && 'line-clamp-2',
            )}
          >
            {body}
          </button>
        )}
        {meta && <p className="text-muted figures mt-1 font-mono text-xs [overflow-wrap:anywhere]">{meta}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {action}
        {menu && (
          <Menu>
            <MenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="data-[state=open]:bg-surface-2 data-[state=open]:text-text size-11 sm:size-8"
                aria-label={menuLabel}
                title="More"
              >
                <MoreHorizontal />
              </Button>
            </MenuTrigger>
            <MenuContent align="end" className="min-w-48">
              {menu}
            </MenuContent>
          </Menu>
        )}
      </div>
      {below && <div className="basis-full pb-1 pl-[3.25rem] sm:pl-11">{below}</div>}
    </li>
  );
}

/** A destructive move asked about once, on its own line under the row. */
export function ConfirmLine({
  question,
  confirm,
  pending,
  onConfirm,
  onCancel,
}: {
  question: string;
  confirm: string;
  pending?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <span className="text-muted mr-1">{question}</span>
      <Button variant="ghost" size="lg" onClick={onCancel}>
        No
      </Button>
      <Button variant="danger" size="lg" disabled={pending} onClick={onConfirm}>
        {confirm}
      </Button>
    </div>
  );
}

/** A group of rows under a heading, as the Tasks page groups its own. */
export function RecordGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-muted text-xs tracking-wide uppercase">{title}</h3>
      {children}
    </section>
  );
}

/**
 * What is kept but no longer current — superseded, finished — folded at the
 * end of the list and counted, so opening it is not a guess. Whether it is
 * open is remembered, per list.
 */
export function Fold({ title, count, storageKey, children }: { title: string; count: number; storageKey: string; children: ReactNode }) {
  const [open, setOpen] = useStoredFlag(storageKey);
  if (count === 0) return null;
  return (
    <section className="space-y-2">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="text-muted hover:text-text -mx-1 flex items-center gap-1 rounded px-1 text-xs tracking-wide uppercase"
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        {title} · <span className="figures">{count}</span>
      </button>
      {open && children}
    </section>
  );
}

function useStoredFlag(key: string): [boolean, (value: boolean) => void] {
  const read = () => {
    try {
      return window.localStorage.getItem(key) === 'true';
    } catch {
      return false;
    }
  };
  const [value, setValue] = useState(read);
  // Another list, another choice.
  const [seenKey, setSeenKey] = useState(key);
  if (seenKey !== key) {
    setSeenKey(key);
    setValue(read());
  }
  const remember = (next: boolean) => {
    setValue(next);
    try {
      window.localStorage.setItem(key, String(next));
    } catch {
      // The choice lasts this visit instead.
    }
  };
  return [value, remember];
}

/** The button in a page's header that makes a new one, with its key. */
export function NewButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button size="lg" className="gap-1.5" title={`${label} (N)`} aria-keyshortcuts="N" onClick={onClick}>
      <Plus />
      {label}
      <kbd className="text-muted border-border ml-1 hidden rounded border px-1 font-sans text-[0.6875rem] leading-4 sm:inline">
        N
      </kbd>
    </Button>
  );
}

/** The panel a new one is written in, with its footer of Cancel and the verb. */
export function NewPanel({
  onSubmit,
  onCancel,
  verb,
  pending,
  ready,
  hint = 'Enter saves · Esc cancels',
  children,
}: {
  onSubmit: () => void;
  onCancel: () => void;
  verb: string;
  pending: boolean;
  ready: boolean;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <form
      className="bg-surface border-border space-y-3 rounded-xl border p-3 shadow-xs"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready && !pending) onSubmit();
      }}
      onKeyDown={(event) => {
        if (event.key === 'Escape' && !event.defaultPrevented) onCancel();
      }}
    >
      {children}
      <div className="border-border flex items-center justify-end gap-2 border-t pt-3">
        <span className="text-muted mr-auto hidden text-xs sm:inline">{hint}</span>
        <Button type="button" variant="ghost" size="lg" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" size="lg" disabled={pending || !ready}>
          {verb}
        </Button>
      </div>
    </form>
  );
}
