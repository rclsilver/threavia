import { Link, useParams } from '@tanstack/react-router';
import { ArrowLeft, Check, ChevronLeft, ChevronRight, Files, MessageSquare, RefreshCw, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useReview, useReviewAction, useReviewFile, useReviewComparison, type ReviewAction, type ReviewThread } from '@/api/reviews';
import { useSnapshot } from '@/api/queries';
import { ReviewCode } from '@/components/review-code';
import { ReviewDraft, type DraftHandle } from '@/components/review-draft';
import { ReviewTree } from '@/components/review-tree';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { uuid } from '@/lib/uuid';

interface Composer {
  key: string; revisionId: string; repositoryId?: string; path?: string;
  side?: 'before' | 'after'; startLine?: number; endLine?: number;
  threadId?: string; messageId?: string; body?: string;
}

export function ReviewView() {
  const { sessionId } = useParams({ from: '/sessions/$sessionId/review' });
  const reviewQuery = useReview(sessionId);
  const snapshot = useSnapshot(sessionId);
  const action = useReviewAction(sessionId);
  const [chosenRevision, setChosenRevision] = useState('');
  const [selection, setSelection] = useState('');
  const [compareTo, setCompareTo] = useState('');
  const [mode, setMode] = useState<'diff' | 'before' | 'after'>('diff');
  const [split, setSplit] = useState(false);
  const [treeOpen, setTreeOpen] = useState(false);
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [lineSelection, setLineSelection] = useState<{ side: 'before' | 'after'; start: number; end: number; anchor: number }>();
  const [extendSelection, setExtendSelection] = useState(false);
  const [discussionsOpen, setDiscussionsOpen] = useState(false);
  const [composer, setComposer] = useState<Composer | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [receipt, setReceipt] = useState('');
  const [directory, setDirectory] = useState('');
  const draft = useRef<DraftHandle>(null);
  const draftIdentity = useRef<{ threadId?: string; messageId?: string }>({});
  const initialCapture = useRef(false);
  const filePanel = useRef<HTMLElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const commentScroll = useRef(0);
  const review = reviewQuery.data;
  const revision = review?.revisions.find((item) => item.id === chosenRevision) ?? review?.revisions.at(-1);
  const comparison = useReviewComparison(sessionId, revision?.id ?? '', compareTo);
  const displayRevision = compareTo ? comparison.data?.revisions.find((item) => item.id === revision?.id) : revision;
  const files = displayRevision?.repositories.flatMap((repo) => repo.files.map((file) => ({ ...file, repositoryId: repo.repositoryId }))) ?? [];
  const selected = files.find((file) => `${file.repositoryId}:${file.path}` === selection) ?? files[0];
  const selectedKey = selected ? `${selected.repositoryId}:${selected.path}` : '';
  const file = useReviewFile(sessionId, revision?.id ?? '', selected?.repositoryId ?? '', selected?.path ?? '', compareTo);
  const [mobile, setMobile] = useState(() => typeof matchMedia !== 'undefined' && matchMedia('(max-width: 767px)').matches);
  useEffect(() => { const media = window.matchMedia('(max-width: 767px)'); const update = () => setMobile(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update); }, []);
  useEffect(() => { setLineSelection(undefined); setExtendSelection(false); }, [revision?.id, selectedKey, compareTo, mode]);

  useEffect(() => {
    if (!review || review.revisions.length || !review.repositories.length || initialCapture.current) return;
    initialCapture.current = true;
    void action.mutateAsync({ action: 'refresh' }).catch(() => undefined);
  }, [review, action]);

  const flush = async () => { await draft.current?.flush(); };
  const run = async (input: Omit<ReviewAction, 'expectedVersion'>, success: string) => {
    setError(null);
    try { await flush(); await action.mutateAsync(input); setReceipt(success); setComposer(null); }
    catch (failure) { setError(failure); }
  };
  const choose = async (repo: string, path: string) => {
    try { await flush(); setComposer(null); setLineSelection(undefined); setExtendSelection(false); setSelection(`${repo}:${path}`); setTreeOpen(false); if (filePanel.current) filePanel.current.scrollTop = 0; }
    catch (failure) { setError(failure); }
  };
  const begin = async (next: Omit<Composer, 'key'>) => {
    try {
      await flush(); draftIdentity.current = { threadId: next.threadId, messageId: next.messageId };
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      commentScroll.current = filePanel.current?.scrollTop ?? 0;
      setComposer({ ...next, key: uuid() });
    } catch (failure) { setError(failure); }
  };
  const selectLine = (side: 'before' | 'after', line: number, extend: boolean) => {
    if (!selected || !revision) return;
    if ((extend || extendSelection) && lineSelection?.side === side) {
      setLineSelection({ side, anchor: lineSelection.anchor, start: Math.min(lineSelection.anchor, line), end: Math.max(lineSelection.anchor, line) });
    } else setLineSelection({ side, anchor: line, start: line, end: line });
    setExtendSelection(false);
  };
  const saveDraft = async (body: string) => {
    if (!composer) return;
    const identity = draftIdentity.current;
    const saved = await action.mutateAsync(identity.messageId ? { action: 'edit', threadId: identity.threadId, messageId: identity.messageId, body } : {
      action: 'comment', revisionId: composer.revisionId, repositoryId: composer.repositoryId, path: composer.path,
      side: composer.side, startLine: composer.startLine, endLine: composer.endLine, threadId: identity.threadId, body,
    });
    const thread = identity.threadId ? saved.threads.find((item) => item.id === identity.threadId) : saved.threads.at(-1);
    draftIdentity.current = { threadId: thread?.id, messageId: identity.messageId ?? thread?.messages.at(-1)?.id };
  };
  const cards = (thread: ReviewThread) => <Thread key={thread.id} thread={thread}
    old={thread.revisionId !== revision?.id} busy={action.isPending}
    reply={() => void begin({ revisionId: thread.revisionId, threadId: thread.id })}
    edit={(messageId, body) => void begin({ revisionId: thread.revisionId, threadId: thread.id, messageId, body })}
    resolve={() => void run({ action: 'resolve', threadId: thread.id, resolved: !thread.resolved }, thread.resolved ? 'Discussion reopened.' : 'Discussion resolved.')}
    remove={(messageId) => void run({ action: 'delete', threadId: thread.id, messageId }, 'Draft discarded.')}
    locate={() => {
      void flush().then(() => { setComposer(null); setChosenRevision(thread.revisionId); setCompareTo(''); if (thread.path) setSelection(`${thread.repositoryId}:${thread.path}`); setDiscussionsOpen(false); }).catch(setError);
    }} />;

  if (!review) return <div className="p-6">{reviewQuery.error ? <ActionError error={reviewQuery.error} outcome="The review did not open" recovery="Reload to try again." /> : <div role="status" className="animate-pulse space-y-3"><div className="h-8 w-48 rounded bg-surface-2" /><div className="h-64 rounded bg-surface-2" /><span className="sr-only">Loading review…</span></div>}</div>;
  const drafts = review.threads.flatMap((thread) => thread.messages).filter((message) => message.draft).length;
  const unresolved = review.threads.filter((thread) => !thread.resolved).length;
  const latest = revision?.id === review.revisions.at(-1)?.id;
  const repo = review.repositories.find((item) => item.id === selected?.repositoryId);
  const selectedIndex = files.findIndex((item) => `${item.repositoryId}:${item.path}` === selectedKey);
  const correctionActive = review.status === 'CORRECTING';
  const showSplit = split && !mobile;

  return <div className="review-view" data-tour="review">
    <header className="review-header">
      <div className="flex min-w-0 items-center gap-3">
        <Button asChild variant="ghost" size="lg"><Link to="/sessions/$sessionId" params={{ sessionId }} onClick={(event) => { if (composer) { event.preventDefault(); void flush().then(() => { window.location.assign(window.location.pathname.replace(/\/review$/, '')); }).catch(setError); } }} aria-label="Back to conversation"><ArrowLeft /></Link></Button>
        <div className="min-w-0"><h1 className="text-base font-semibold">Code review</h1><p className="truncate text-xs text-muted">{snapshot.data?.session.title ?? 'Session changes'}</p></div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={review.required ? 'warn' : review.status === 'APPROVED' ? 'ok' : 'neutral'}>{correctionActive ? 'Correcting' : review.required ? 'Validation required' : review.status === 'APPROVED' ? 'Approved' : 'Browse changes'}</Badge>
        <Button size="lg" disabled={action.isPending || correctionActive} onClick={() => void run({ action: 'refresh' }, 'Changes captured.')}><RefreshCw />Refresh</Button>
      </div>
    </header>
    <details className="review-options" open={!mobile || optionsOpen} onToggle={(event) => { if (mobile && event.currentTarget.open !== optionsOpen) setOptionsOpen(event.currentTarget.open); }}>
      <summary>Review options · revision {review.revisions.findIndex((item) => item.id === revision?.id) + 1}</summary>
      {review.required && <p className="review-waiting" role="status">{correctionActive ? 'Only the requested correction turn may run. Your validation is needed afterwards.' : 'Queued work is blocked. A connected agent pauses before its next action, until you approve this review.'}</p>}
    <div className="review-toolbar">
      <label className="review-select-label">Revision<select aria-label="Revision" value={revision?.id ?? ''} onChange={(event) => { const value = event.target.value; void flush().then(() => { setChosenRevision(value === review.revisions.at(-1)?.id ? '' : value); setCompareTo(''); setComposer(null); }).catch(setError); }}>
        {review.revisions.map((item, i) => <option key={item.id} value={item.id}>{i + 1}{item.approved ? ' · approved' : ''}{i === review.revisions.length - 1 ? ' · latest' : ''}</option>)}
      </select></label>
      <label className="review-select-label">Compare<select aria-label="Comparison baseline" value={compareTo} onChange={(event) => { const value = event.target.value; void flush().then(() => { setCompareTo(value); setComposer(null); }).catch(setError); }}>
        <option value="">Since last approval</option><option value="initial">Since tracking began</option>
        {review.revisions.slice(0, review.revisions.findIndex((item) => item.id === revision?.id)).map((item, i) => <option key={item.id} value={item.id}>Since revision {i + 1}</option>)}
      </select></label>
      <Button size="lg" variant="ghost" className="ml-auto" aria-expanded={discussionsOpen} onClick={() => setDiscussionsOpen(!discussionsOpen)}><MessageSquare /><span className="figures">{unresolved}</span> open</Button>
      <Button size="lg" variant="ghost" disabled={!revision} onClick={() => void begin({ revisionId: revision?.id ?? '' })}>Review comment</Button>
    </div>
    </details>
    <div className="px-4"><ActionError error={error ? (error instanceof Error ? error : new Error(typeof error === 'string' ? error : 'Unexpected error')) : action.error} outcome="The review action did not complete" recovery="Your draft text is preserved. Reload the review state and retry." /><p className="text-xs text-muted" role="status">{receipt}</p></div>
    <div className="review-selection-bar" aria-label="Comment line selection">
      {lineSelection && selected ? <><span className="font-mono text-xs">{lineSelection.side} {lineSelection.start}{lineSelection.end !== lineSelection.start ? `–${lineSelection.end}` : ''}</span>
        <Button size="lg" aria-pressed={extendSelection} onClick={() => setExtendSelection(!extendSelection)}>{extendSelection ? 'Tap last line' : 'Range'}</Button>
        <Button size="lg" onClick={() => void begin({ revisionId: revision?.id ?? '', repositoryId: selected.repositoryId, path: selected.path, side: lineSelection.side, startLine: lineSelection.start, endLine: lineSelection.end })}>Comment</Button>
        <Button size="lg" variant="ghost" aria-label="Clear line selection" onClick={() => { setLineSelection(undefined); setExtendSelection(false); }}><X /></Button>
      </> : <span className="text-xs text-muted">Select a line number, then Comment. Use Range to choose an end line.</span>}
    </div>
    <div className="review-workspace">
      <aside className="review-files-panel">
        <div className="review-panel-heading"><h2>Changed files</h2><span className="figures">{files.length}</span></div>
        <ReviewTree review={review} revision={displayRevision} selected={selectedKey} choose={(repository, path) => void choose(repository, path)} disabled={action.isPending} />
        {compareTo && comparison.error && <ActionError error={comparison.error} outcome="The comparison did not load" recovery="Reconnect the backend and choose the comparison again." />}
        {!review.repositories.length && <form className="space-y-3 p-3" onSubmit={(event) => { event.preventDefault(); void run({ action: 'register', directory }, 'Repository registered from its current state.'); }}>
          <p className="text-sm text-muted">The agent captures its repository before starting work. You can also declare an extra repository here.</p>
          <label className="block text-sm">Repository path<input className="mt-1 w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-xs" value={directory} onChange={(event) => setDirectory(event.target.value)} placeholder="/path/to/worktree" required /></label>
          <Button size="lg" type="submit" disabled={action.isPending}>Track repository</Button>
        </form>}
      </aside>
      <main ref={filePanel} className="review-file-panel">
        {selected ? <>
          <div className="review-file-controls">
          <div className="review-file-heading">
            <Button size="lg" className="md:hidden" aria-label="Choose file" onClick={() => setTreeOpen(true)}><Files />Files</Button>
            <div className="min-w-0 flex-1"><p className="text-xs text-muted">{repo?.name} · {selectedIndex + 1}/{files.length}</p><h2 className="truncate font-mono text-sm" title={selected.path}>{selected.path}</h2>{selected.oldPath && <p className="truncate font-mono text-xs text-muted">Renamed from {selected.oldPath}</p>}</div>
            <Button size="lg" aria-label="Previous file" disabled={selectedIndex <= 0} onClick={() => { const target = files[selectedIndex - 1]; if (target) void choose(target.repositoryId, target.path); }}><ChevronLeft /></Button>
            <Button size="lg" aria-label="Next file" disabled={selectedIndex >= files.length - 1} onClick={() => { const target = files[selectedIndex + 1]; if (target) void choose(target.repositoryId, target.path); }}><ChevronRight /></Button>
          </div>
          <div className="review-file-tools">
            <Button size="lg" aria-label="Mark file viewed" disabled={action.isPending} onClick={() => void run({ action: 'seen', revisionId: revision?.id, repositoryId: selected.repositoryId, path: selected.path }, 'File marked viewed.')}><Check /><span className="hidden md:inline">Viewed</span></Button>
            <label className="review-select-label"><span className="max-md:sr-only">View</span><select aria-label="File view" value={mode} onChange={(event) => setMode(event.target.value as typeof mode)}><option value="diff">Diff</option><option value="after">Full file · after</option><option value="before">Full file · before</option></select></label>
            <Button size="lg" variant="ghost" className="hidden md:inline-flex" aria-pressed={split} onClick={() => setSplit(!split)}>{split ? 'Unified' : 'Side by side'}</Button>
            <Button size="lg" variant="ghost" className="ml-auto" aria-label="Comment on file" onClick={() => void begin({ revisionId: revision?.id ?? '', repositoryId: selected.repositoryId, path: selected.path })}><MessageSquare /><span className="hidden md:inline">Comment on file</span></Button>
          </div>
          </div>
          {compareTo && <p className="px-4 py-2 text-xs text-muted">Comments on the current side use its original file line numbers. For removed lines, choose “Since last approval”.</p>}
          {file.isPending ? <div role="status" className="p-4 text-sm text-muted">Loading the captured file…</div> : file.error ? <div className="p-4"><ActionError error={file.error} outcome="The captured file could not be loaded" recovery="Reconnect its backend and retry. Your comments are still saved." /><Button size="lg" onClick={() => void file.refetch()}>Retry loading file</Button></div> : file.data && <ReviewCode content={file.data} mode={mode} split={showSplit} canSelectBefore={!compareTo} select={selectLine}
            selected={lineSelection}
            afterLine={(side, line) => !compareTo && review.threads.filter((thread) => thread.path === selected.path && thread.repositoryId === selected.repositoryId && thread.revisionId === revision?.id && thread.side === side && thread.endLine === line).map(cards)} />}
          {review.threads.filter((thread) => thread.path === selected.path && thread.repositoryId === selected.repositoryId && !thread.startLine).map(cards)}

        </> : <div className="review-empty"><Files className="size-7 text-muted" /><h2 className="font-medium">No captured changes</h2><Button size="lg" className="md:hidden" onClick={() => setTreeOpen(true)}>Files and repositories</Button><p className="max-w-sm text-sm text-muted">{review.repositories.length ? 'Refresh after the agent changes a file. Earlier revisions remain available above.' : 'Repositories are tracked when work starts. Declare any additional worktree before editing it.'}</p></div>}

        {discussionsOpen && <section className="review-discussions" aria-label="All discussions"><h2 className="p-4 font-medium">Discussions across revisions</h2>{review.threads.length ? review.threads.map(cards) : <p className="p-4 text-sm text-muted">Tap a line number to start a discussion, or add a review comment.</p>}</section>}
      </main>
    </div>
    <Dialog open={treeOpen && mobile} onOpenChange={setTreeOpen}>
      <DialogContent className="review-dialog review-file-picker" onCloseAutoFocus={(event) => { event.preventDefault(); filePanel.current?.querySelector<HTMLButtonElement>('button')?.focus({ preventScroll: true }); }}>
        <DialogTitle>Files · {files.length}</DialogTitle>
        <DialogDescription>Choose a repository and file. The code stays in place until you choose.</DialogDescription>
        <ReviewTree review={review} revision={displayRevision} selected={selectedKey} choose={(repository, path) => void choose(repository, path)} disabled={action.isPending} />
      </DialogContent>
    </Dialog>
    <Dialog open={Boolean(composer)} onOpenChange={(open) => { if (!open) void flush().then(() => setComposer(null)).catch(setError); }}>
      <DialogContent className="review-dialog" onOpenAutoFocus={(event) => { event.preventDefault(); document.querySelector<HTMLElement>('[data-review-editor-heading]')?.focus({ preventScroll: true }); }} onCloseAutoFocus={(event) => { event.preventDefault(); if (filePanel.current) filePanel.current.scrollTop = commentScroll.current; returnFocus.current?.focus({ preventScroll: true }); }}>
        <DialogTitle data-review-editor-heading tabIndex={-1}>{composer?.path ?? 'Review comment'}</DialogTitle>
        <DialogDescription>Saved as a draft. Only sent when you request corrections.</DialogDescription>
        {composer?.side && composer.startLine && <pre className="review-comment-source"><code>{composer.path === selected?.path && composer.repositoryId === selected?.repositoryId && composer.revisionId === revision?.id && file.data ? file.data[composer.side].split('\n').slice(composer.startLine - 1, Math.min(composer.endLine ?? composer.startLine, composer.startLine + 19)).join('\n') : review.threads.find((thread) => thread.id === composer.threadId)?.excerpt}</code></pre>}
        {composer && (<div className="review-compose-area">
          {composer.startLine && <div className="flex flex-wrap items-center gap-2 pb-2 text-xs"><span>{composer.side} lines</span><input aria-label="First comment line" type="number" min={1} value={composer.startLine} disabled={Boolean(draftIdentity.current.messageId)} onChange={(event) => setComposer({ ...composer, startLine: Number(event.target.value) })} /><span>to</span><input aria-label="Last comment line" type="number" min={composer.startLine} value={composer.endLine} disabled={Boolean(draftIdentity.current.messageId)} onChange={(event) => setComposer({ ...composer, endLine: Number(event.target.value) })} /></div>}
          <ReviewDraft key={composer.key} ref={draft} initialBody={composer.body} label={composer.threadId ? 'Reply to discussion' : composer.path ? `${composer.path}${composer.startLine ? ` · line ${composer.startLine}` : ''}` : 'Comment on this review'} save={saveDraft} close={() => setComposer(null)} />
        </div>)}
      </DialogContent>
    </Dialog>
    <footer className="review-footer">
      <div className="hidden flex-wrap items-center gap-2 md:flex"><Button size="lg" variant="ghost" disabled={!revision} onClick={() => void begin({ revisionId: revision?.id ?? '' })}>Review comment</Button><span className="text-xs text-muted figures">{drafts} draft{drafts === 1 ? '' : 's'}</span></div>
      <div className="flex flex-wrap items-center gap-2">
        {!review.required && <Button size="lg" disabled={action.isPending} onClick={() => void run({ action: 'require' }, 'Validation is now required before the agent continues.')}>Require validation</Button>}
        <Button size="lg" disabled={action.isPending || correctionActive || !latest || (!drafts && !composer)} onClick={() => void run({ action: 'publish', revisionId: revision?.id, batchId: uuid() }, 'Review sent. The correction turn is queued.')} data-tour="review-send">Request corrections <span className="md:hidden">({drafts})</span></Button>
        <Button size="lg" variant="primary" disabled={action.isPending || correctionActive || !latest || !revision || unresolved > 0 || drafts > 0 || revision.repositories.some((item) => Boolean(item.error))} onClick={() => void run({ action: 'approve', revisionId: revision?.id }, 'Review approved. The agent may continue. No merge was performed.')}>Approve review</Button>
      </div>
    </footer>
  </div>;
}

function Thread({ thread, old, busy, reply, edit, resolve, remove, locate }: {
  thread: ReviewThread; old: boolean; busy: boolean; reply: () => void; edit: (id: string, body: string) => void;
  resolve: () => void; remove: (id: string) => void; locate: () => void;
}) {
  return <article className={cn('review-thread', thread.resolved && 'is-resolved')} aria-label="Code discussion">
    <div className="flex flex-wrap items-center justify-between gap-2 text-xs"><span className="font-medium">{thread.resolved ? 'Resolved' : 'Open discussion'}{old ? ' · earlier revision' : ''}</span><Button size="lg" variant="ghost" disabled={busy} onClick={resolve}>{thread.resolved ? 'Reopen' : 'Resolve'}</Button></div>
    {(thread.path || old) && <button type="button" className="review-thread-location" onClick={locate}>{thread.path ?? 'Review comment'}{thread.startLine ? ` · ${thread.side} ${thread.startLine}${thread.endLine !== thread.startLine ? `–${thread.endLine}` : ''}` : ''}</button>}
    {old && thread.excerpt && <pre className="overflow-x-auto rounded bg-surface-2 p-2 text-xs"><code>{thread.excerpt}</code></pre>}
    {thread.messages.map((message) => <div key={message.id} className="review-thread-message"><p className="text-xs text-muted">{message.author === 'user' ? 'You' : 'Agent'}{message.draft ? ' · draft' : ''}</p><p className="whitespace-pre-wrap break-words text-sm">{message.body}</p>{message.draft && <div className="flex gap-2"><Button size="lg" variant="ghost" disabled={busy} onClick={() => edit(message.id, message.body)}>Edit draft</Button><Button size="lg" variant="ghost" disabled={busy} onClick={() => remove(message.id)}>Discard draft</Button></div>}</div>)}
    <Button size="lg" variant="ghost" disabled={busy} onClick={reply}>Reply</Button>
  </article>;
}
