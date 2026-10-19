import { Fragment } from 'react';
import { reviewLines, splitReviewLines, wholeFileLines, type ReviewLine } from '../../../../clients/shared/review';
import type { ReviewFileContent } from '@/api/reviews';

export function ReviewCode({ content, mode, split, select, selected, canSelectBefore, afterLine }: {
  content: ReviewFileContent; mode: 'diff' | 'before' | 'after'; split: boolean;
  select: (side: 'before' | 'after', line: number, extend: boolean) => void;
  selected?: { side: string; start: number; end: number };
  canSelectBefore: boolean;
  afterLine: (side: 'before' | 'after', line: number) => React.ReactNode;
}) {
  const lines = mode === 'diff' ? reviewLines(content.diff) : wholeFileLines(content[mode], mode);
  const gutter = (side: 'before' | 'after', number: number | undefined) => number === undefined ? <span className="review-line-number" /> : <button
    type="button" className="review-line-number" aria-label={`Comment on ${side} line ${number}`}
    disabled={content.binary || content.truncated || (side === 'before' && !canSelectBefore)}
    aria-pressed={selected?.side === side && number >= selected.start && number <= selected.end}
    onClick={(event) => select(side, number, event.shiftKey)}>{number}</button>;
  const row = (line: ReviewLine, side?: 'before' | 'after') => <div className={`review-code-row review-code-${line.kind}`}>
    {!side || side === 'before' ? gutter('before', line.before) : null}
    {!side || side === 'after' ? gutter('after', line.after) : null}
    <span className="review-line-sign" aria-hidden="true">{line.kind === 'added' ? '+' : line.kind === 'deleted' ? '−' : ' '}</span>
    <code>{line.text || ' '}</code>
  </div>;

  if (content.binary) return <p className="p-6 text-muted">Binary file. Line comments are unavailable; use a file comment.</p>;
  if (content.truncated) return <p className="p-6 text-warn-text">This file exceeds the text preview limit. Use a file comment and inspect it on the backend before approving.</p>;
  if (!lines.length) return <p className="p-6 text-muted">{mode === 'diff' ? 'No text changes in this comparison. Open the full file for context.' : 'This side of the file is empty.'}</p>;
  return <div className="review-code" aria-label="Code with commentable line numbers">
    {split && mode === 'diff' ? <div className="review-split">{splitReviewLines(lines).map((pair, i) => pair.hunk ? <div key={i} className="review-hunk">{pair.hunk}</div> : <Fragment key={i}>
      <div>{pair.before ? row(pair.before, 'before') : <div className="review-code-row" />}</div>
      <div>{pair.after ? row(pair.after, 'after') : <div className="review-code-row" />}</div>
      {pair.before?.before && <div className="review-inline-thread">{afterLine('before', pair.before.before)}</div>}
      {pair.after?.after && <div className="review-inline-thread">{afterLine('after', pair.after.after)}</div>}
    </Fragment>)}</div> : lines.map((line, i) => line.kind === 'hunk' ? <div key={i} className="review-hunk">{line.text}</div> : <Fragment key={i}>
      {row(line)}
      {line.before !== undefined && afterLine('before', line.before)}
      {line.after !== undefined && afterLine('after', line.after)}
    </Fragment>)}
  </div>;
}
