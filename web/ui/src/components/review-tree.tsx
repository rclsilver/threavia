import { Check, ChevronRight, FileCode2, Folder } from 'lucide-react';
import { reviewTree, type ReviewTreeNode } from '../../../../clients/shared/review';
import type { Review } from '@/api/reviews';
import { cn } from '@/lib/utils';

export function ReviewTree({ review, revision, selected, choose, disabled }: {
  review: Review;
  revision: Review['revisions'][number] | undefined;
  selected: string;
  choose: (repositoryId: string, path: string) => void;
  disabled: boolean;
}) {
  return <nav aria-label="Changed files" className="review-tree" data-tour="review-tree">
    {review.repositories.map((repo) => {
      const state = revision?.repositories.find((item) => item.repositoryId === repo.id);
      return <section key={repo.id}>
        <div className="review-repository"><Folder className="size-4 shrink-0" /><strong>{repo.name}</strong><span className="text-muted figures">{state?.files.length ?? 0}</span></div>
        <p className="review-repository-path" title={repo.directory}>{repo.directory}</p>
        {repo.baselineSource === 'DECLARED' && <p className="px-3 pb-2 text-xs text-muted">Tracked from declaration</p>}
        {state?.error ? <p role="status" className="px-3 py-2 text-xs text-warn-text">{state.error}</p> : state?.files.length ? <TreeNodes nodes={reviewTree(state.files)} repoId={repo.id} selected={selected} choose={choose} seen={review.seen} tree={state.headTree} disabled={disabled} /> : <p className="px-3 pb-3 text-xs text-muted">No captured changes.</p>}
      </section>;
    })}
  </nav>;
}

function TreeNodes({ nodes, repoId, selected, choose, seen, tree, disabled }: {
  nodes: ReviewTreeNode[]; repoId: string; selected: string; choose: (repo: string, path: string) => void;
  seen: Record<string, string>; tree: string; disabled: boolean;
}) {
  return <ul>{nodes.map((node) => <li key={node.path}>
    {node.file ? <button type="button" disabled={disabled} className={cn('review-file', selected === `${repoId}:${node.path}` && 'is-selected')}
      aria-current={selected === `${repoId}:${node.path}` ? 'page' : undefined} onClick={() => choose(repoId, node.path)} title={node.file.oldPath ? `${node.file.oldPath} → ${node.path}` : node.path}>
      <FileCode2 className="size-4 shrink-0" /><span className="truncate font-mono text-xs">{node.name}</span>
      <span className={cn('ml-auto text-xs', node.file.state === 'ADDED' ? 'text-ok' : node.file.state === 'DELETED' ? 'text-danger' : 'text-muted')}>{node.file.state.slice(0, 1)}</span>
      {seen[`${repoId}:${node.path}`] === tree && <Check className="size-3 text-ok" aria-label="Viewed" />}
    </button> : <details open><summary><ChevronRight className="review-folder-chevron size-3" /><Folder className="size-4" /><span className="font-mono text-xs">{node.name}</span></summary><div className="pl-3"><TreeNodes nodes={node.children} repoId={repoId} selected={selected} choose={choose} seen={seen} tree={tree} disabled={disabled} /></div></details>}
  </li>)}</ul>;
}
