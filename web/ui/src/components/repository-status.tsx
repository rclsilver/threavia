import { ArrowDown, ArrowUp, GitBranch, RefreshCw } from 'lucide-react';
import type { ReactNode } from 'react';

import { useFetchRepository, useRepository } from '@/api/queries';
import type { Repository } from '@/api/types';
import { ActionError } from '@/components/ui/action-error';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from '@/components/ui/menu';
import { ago, cn } from '@/lib/utils';

/**
 * Where the Session's repository stands, in the line under its title: the
 * branch, whether it is ahead or behind its upstream, and whether anything is
 * left to commit. Opened, it says each in words, and when origin was last
 * heard from — the one thing a glance cannot vouch for — with a refresh that
 * asks origin now.
 *
 * Nothing at all for a working directory outside git, or for one that cannot
 * be read at the moment: the line already says which machine holds the work.
 */
export function RepositoryStatus({ sessionId }: { sessionId: string }) {
  const repository = useRepository(sessionId);
  const fetch = useFetchRepository(sessionId);
  const repo = repository.data;
  if (!repo?.tracked) return null;

  const changes = repo.staged + repo.unstaged + repo.untracked + repo.conflicted;
  return (
    <Menu>
      <MenuTrigger
        title="Where the repository stands"
        className="hover:bg-surface-2 hover:text-text data-[state=open]:bg-surface-2 data-[state=open]:text-text -my-0.5 flex min-w-0 items-center gap-1.5 rounded px-1 py-0.5 max-sm:min-h-8"
      >
        <GitBranch className="size-3 shrink-0" />
        <span className="max-w-40 truncate font-mono">{repo.branch || repo.head || 'no commit'}</span>
        {repo.upstream && !repo.upstreamGone && (repo.ahead > 0 || repo.behind > 0) && (
          <span className="figures flex items-center gap-1">
            {repo.ahead > 0 && (
              <span className="flex items-center" aria-label={`${repo.ahead} ahead`}>
                <ArrowUp className="size-3" />
                {repo.ahead}
              </span>
            )}
            {repo.behind > 0 && (
              <span className="text-warn-text flex items-center" aria-label={`${repo.behind} behind`}>
                <ArrowDown className="size-3" />
                {repo.behind}
              </span>
            )}
          </span>
        )}
        {changes > 0 && (
          <span
            className={cn('size-1.5 shrink-0 rounded-full', repo.conflicted ? 'bg-danger' : 'bg-warn')}
            aria-label={`${changes} not committed`}
          />
        )}
      </MenuTrigger>
      <MenuContent align="start" className="w-[min(20rem,calc(100vw-1.5rem))] p-0">
        <div className="space-y-3 p-3">
          <div className="min-w-0">
            <p className="flex items-center gap-1.5 text-sm font-medium">
              <GitBranch className="text-muted size-3.5 shrink-0" />
              <span className="truncate font-mono">{repo.branch || `detached at ${repo.head}`}</span>
            </p>
            <p className="text-muted mt-0.5 truncate font-mono text-xs" title={repo.directory}>
              {repo.directory}
            </p>
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
            <Fact term="Upstream">{upstreamLine(repo)}</Fact>
            <Fact term="Changes">{changesLine(repo)}</Fact>
            <Fact term="Fetched">
              {repo.fetchedAt ? ago(repo.fetchedAt) : 'never'}
              {repo.fetchError && <span className="text-danger block">{repo.fetchError}</span>}
            </Fact>
          </dl>
          <ActionError error={fetch.error} outcome="Not refreshed" className="text-xs" />
        </div>
        <MenuSeparator className="mx-0 my-0" />
        <div className="p-1">
          <MenuItem
            disabled={fetch.isPending}
            onSelect={(event) => {
              // Stays open, so the answer is read where it was asked for.
              event.preventDefault();
              fetch.mutate();
            }}
          >
            <RefreshCw className={cn('text-muted size-3.5', fetch.isPending && 'animate-spin motion-reduce:animate-none')} />
            {fetch.isPending ? 'Asking origin…' : 'Refresh from origin'}
          </MenuItem>
        </div>
      </MenuContent>
    </Menu>
  );
}

function Fact({ term, children }: { term: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted">{term}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

function upstreamLine(repo: Repository): ReactNode {
  if (!repo.upstream) return <span className="text-muted">This branch tracks nothing</span>;
  const name = <span className="font-mono">{repo.upstream}</span>;
  if (repo.upstreamGone) return <>{name} no longer exists</>;
  if (repo.ahead === 0 && repo.behind === 0) return <>Level with {name}</>;
  const parts = [
    repo.ahead > 0 && `${repo.ahead} to push`,
    repo.behind > 0 && `${repo.behind} to pull`,
  ].filter(Boolean);
  return (
    <>
      <span className={cn(repo.behind > 0 && 'text-warn-text')}>{parts.join(', ')}</span>
      <span className="text-muted block truncate">{name}</span>
    </>
  );
}

function changesLine(repo: Repository): ReactNode {
  const parts = [
    repo.conflicted > 0 && <span key="c" className="text-danger">{repo.conflicted} in conflict</span>,
    repo.staged > 0 && <span key="s">{repo.staged} staged</span>,
    repo.unstaged > 0 && <span key="m">{repo.unstaged} modified</span>,
    repo.untracked > 0 && <span key="u">{repo.untracked} untracked</span>,
  ].filter(Boolean);
  if (parts.length === 0) return <span className="text-ok">Nothing to commit</span>;
  return (
    <span className="figures">
      {parts.map((part, index) => (
        <span key={index}>
          {index > 0 && <span className="text-muted"> · </span>}
          {part}
        </span>
      ))}
    </span>
  );
}
