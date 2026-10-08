import { Check, ChevronRight, Folder, MessageSquare, Server, X } from 'lucide-react';
import { useEffect, useState, useSyncExternalStore } from 'react';

import { useResolveUserInput, useResolveValidation } from '@/api/queries';
import type { UserInputRequest, ValidationRequest } from '@/api/types';
import { FileChangeView } from '@/components/file-change';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { fileChangeOf } from '@/lib/file-change';
import { headlineOf, riskOf, toolLabel } from '@/lib/risk';
import { ago, cn, when } from '@/lib/utils';

/** A decision just taken, shown for a moment where its card was. */
type Receipt = { id: string; text: string; approved: boolean };

/**
 * The card disappears the moment a decision lands — Core resolves it and every
 * client drops it — so the receipt lives outside the card, for long enough to
 * read that it went through.
 */
let receiptList: Receipt[] = [];
const receiptListeners = new Set<() => void>();

function publishReceipts(next: Receipt[]) {
  receiptList = next;
  receiptListeners.forEach((listener) => listener());
}

function addReceipt(receipt: Receipt) {
  publishReceipts([...receiptList.filter((r) => r.id !== receipt.id), receipt]);
  setTimeout(() => publishReceipts(receiptList.filter((r) => r.id !== receipt.id)), 4000);
}

function subscribeReceipts(listener: () => void) {
  receiptListeners.add(listener);
  return () => {
    receiptListeners.delete(listener);
  };
}

function useReceipts() {
  return useSyncExternalStore(subscribeReceipts, () => receiptList);
}

/**
 * What is waiting for the user, right where the work is.
 *
 * Pending attention is current state, not a count of unread events: a request
 * answered on another device disappears here the moment the stream says so
 * (spec section 6). In a Session the cards sit above the composer; in the
 * Waiting inbox, across every project, each card also says which session it
 * belongs to.
 */
export function AttentionPanel({
  validations,
  userInputs,
  showSession = false,
  className,
}: {
  validations: ValidationRequest[];
  userInputs: UserInputRequest[];
  showSession?: boolean;
  className?: string;
}) {
  const recent = useReceipts();
  if (validations.length === 0 && userInputs.length === 0 && recent.length === 0) return null;

  return (
    <div data-tour="attention" className={cn('space-y-2', className)}>
      {validations.map((request) => (
        <ValidationCard key={request.id} request={request} showSession={showSession} />
      ))}
      {userInputs.map((request) => (
        <UserInputCard key={request.id} request={request} showSession={showSession} />
      ))}
      <div role="status" aria-live="polite" className="space-y-1">
        {recent.map((receipt) => (
          <p
            key={receipt.id}
            className={cn(
              'flex items-center gap-2 text-sm',
              receipt.approved ? 'text-ok' : 'text-muted',
            )}
          >
            {receipt.approved ? <Check className="size-4" /> : <X className="size-4" />}
            <span className="min-w-0 truncate">{receipt.text}</span>
          </p>
        ))}
      </div>
    </div>
  );
}

/** Where a request comes from: the machine, the directory, the session. */
function ContextStrip({
  context,
  showSession,
}: {
  context: ValidationRequest['context'];
  showSession: boolean;
}) {
  if (!context) return null;
  return (
    <p className="text-muted flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
      <span className="flex items-center gap-1" title="The machine that would run it">
        <Server className="size-3.5" />
        {context.backendName}
      </span>
      {context.directory && (
        <span className="flex items-center gap-1" title="The session's working directory">
          <Folder className="size-3.5" />
          <span className="font-mono">{context.directory}</span>
        </span>
      )}
      {showSession && (
        <span className="flex min-w-0 items-center gap-1">
          <MessageSquare className="size-3.5" />
          <span className="truncate">
            {context.projectName} · {context.sessionTitle || 'Untitled session'}
          </span>
        </span>
      )}
    </p>
  );
}

/**
 * A permission request, the product's highest-stakes moment.
 *
 * It is read before it is answered: what would run, on which machine, in
 * which directory, at what risk — the command itself as the headline rather
 * than a JSON payload to decode. The two answers are far enough apart to be
 * chosen with a thumb, and approving something dangerous is coloured as
 * dangerous.
 */
function ValidationCard({ request, showSession }: { request: ValidationRequest; showSession: boolean }) {
  const resolve = useResolveValidation();
  const payload = request.requestPayload as { tool?: unknown; input?: unknown };
  const tool = typeof payload.tool === 'string' ? payload.tool : '';
  const input =
    payload.input && typeof payload.input === 'object' ? (payload.input as Record<string, unknown>) : {};
  const change = tool ? fileChangeOf(tool, input) : null;
  const risk = riskOf(tool, input);
  const headline = headlineOf(input);

  // Which answer was tried, so a failure can say which did not land.
  const [tried, setTried] = useState<boolean | null>(null);
  const decide = (approved: boolean) => {
    setTried(approved);
    resolve.mutate(
      { id: request.id, approved },
      {
        onSuccess: () =>
          addReceipt({
            id: request.id,
            approved,
            text: `${approved ? 'Allowed' : 'Denied'} ${toolLabel(tool) || request.title}${
              headline ? `: ${headline.split('\n')[0]}` : ''
            }${approved ? ' — the agent continues' : ''}`,
          }),
      },
    );
  };

  return (
    <section
      aria-label={`Approval needed: ${request.title}`}
      className={cn(
        'bg-surface rounded-(--radius-card) border p-3 shadow-sm',
        risk.tone === 'danger' ? 'border-danger/50' : 'border-border',
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={risk.tone === 'neutral' ? 'neutral' : risk.tone}>{risk.label}</Badge>
        {tool && <span className="text-muted text-xs">{toolLabel(tool)}</span>}
        <time dateTime={request.createdAt} title={when(request.createdAt)} className="text-muted ml-auto text-xs">
          {ago(request.createdAt)}
        </time>
      </div>

      {change ? (
        <FileChangeView change={change} className="mt-2" />
      ) : (
        <h3 className="mt-2 font-mono text-sm leading-relaxed break-words whitespace-pre-wrap">
          {headline || request.title}
        </h3>
      )}
      {request.summary && !headline && <p className="text-muted mt-1 text-sm">{request.summary}</p>}

      <div className="mt-2">
        <ContextStrip context={request.context} showSession={showSession} />
      </div>

      <details className="group mt-2">
        <summary className="text-muted hover:text-text flex min-h-11 w-fit cursor-pointer list-none items-center gap-1 text-xs sm:min-h-0">
          <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" />
          Full request
        </summary>
        <pre className="bg-surface-2 mt-1.5 max-h-60 overflow-auto rounded-md p-2 font-mono text-xs">
          {JSON.stringify(request.requestPayload, null, 2)}
        </pre>
      </details>

      <div className="mt-3 grid grid-cols-2 gap-3 sm:flex sm:justify-end">
        <Button size="lg" disabled={resolve.isPending} onClick={() => decide(false)}>
          Deny
        </Button>
        <Button
          size="lg"
          variant={risk.tone === 'danger' ? 'danger' : 'primary'}
          disabled={resolve.isPending}
          onClick={() => decide(true)}
        >
          {resolve.isPending ? 'Sending…' : 'Approve'}
        </Button>
      </div>
      <ActionError
        error={resolve.error}
        outcome={tried === false ? 'Not denied' : 'Not approved'}
        recovery="The request is still waiting; decide again once that is resolved."
        className="mt-2"
      />
    </section>
  );
}

/** Whether the pointer is precise: a phone's keyboard must not open by itself. */
function usePreciseInput() {
  const [precise, setPrecise] = useState(() => window.matchMedia('(pointer: fine)').matches);
  useEffect(() => {
    const media = window.matchMedia('(pointer: fine)');
    const onChange = () => setPrecise(media.matches);
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, []);
  return precise;
}

function UserInputCard({ request, showSession }: { request: UserInputRequest; showSession: boolean }) {
  const resolve = useResolveUserInput();
  const [value, setValue] = useState('');
  const choices = request.choices ?? [];
  // On a phone the keyboard would open over the question and its choices.
  const precise = usePreciseInput();

  const answer = (text: string) =>
    resolve.mutate(
      { id: request.id, value: text },
      { onSuccess: () => addReceipt({ id: request.id, approved: true, text: `Answered: ${text}` }) },
    );

  return (
    <section
      aria-label="Question from the agent"
      className="bg-surface border-border rounded-(--radius-card) border p-3 shadow-sm"
    >
      <div className="flex items-center gap-2">
        <Badge tone="accent">Question</Badge>
        <time dateTime={request.createdAt} title={when(request.createdAt)} className="text-muted ml-auto text-xs">
          {ago(request.createdAt)}
        </time>
      </div>
      <h3 className="mt-2 text-sm leading-relaxed font-medium whitespace-pre-wrap">{request.prompt}</h3>
      <div className="mt-2">
        <ContextStrip context={request.context} showSession={showSession} />
      </div>

      {choices.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-2">
          {choices.map((choice) => (
            <Button key={choice} size="lg" disabled={resolve.isPending} onClick={() => answer(choice)}>
              {choice}
            </Button>
          ))}
        </div>
      )}

      {(request.freeText || choices.length === 0) && (
        <form
          className="mt-3 flex gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            const text = value.trim();
            if (text) answer(text);
          }}
        >
          <Input
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="Your answer"
            aria-label="Your answer"
            autoFocus={precise}
            className="h-11 sm:h-9"
          />
          <Button type="submit" variant="primary" size="lg" disabled={resolve.isPending || !value.trim()}>
            Answer
          </Button>
        </form>
      )}
      <ActionError
        error={resolve.error}
        outcome="Not answered"
        recovery="The question is still waiting; answer again once that is resolved."
        className="mt-2"
      />
    </section>
  );
}
