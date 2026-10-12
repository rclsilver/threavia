import type { Repository } from '../api/types';
import { ago } from '../tree/model';
import { h, icon } from './dom';

/** The same status as the header, laid out for reading rather than a title. */
export function repositoryPanel(repo: Repository): HTMLElement {
  const changes = ([
    ['conflicted', 'in conflict', 'warning'],
    ['staged', 'staged', ''],
    ['unstaged', 'modified', ''],
    ['untracked', 'untracked', ''],
  ] as const).flatMap(([field, label, tone]) => repo[field] > 0
    ? [h('span', { class: `repository-change ${tone}` }, h('strong', {}, String(repo[field])), ` ${label}`)]
    : []);

  const tracked = repo.upstream && !repo.upstreamGone;
  return h('div', {},
    h('div', { class: 'repository-heading' }, icon('git-branch'),
      h('strong', {}, repo.branch || 'Detached HEAD')),
    h('dl', { class: 'repository-facts' },
      fact('Commit', repo.head ? h('code', {}, repo.head) : 'No commit yet'),
      fact('Upstream', repo.upstream ? h('code', {}, repo.upstream) : 'Not tracking a branch'),
    ),
    repo.upstreamGone && h('p', { class: 'repository-error' }, icon('warning'), 'The tracked branch no longer exists.'),
    tracked && h('div', { class: 'repository-sync' },
      repo.ahead === 0 && repo.behind === 0
        ? h('span', {}, icon('check'), 'Up to date')
        : [
          h('span', {}, icon('arrow-up'), h('strong', {}, String(repo.ahead)), ' to push'),
          h('span', {}, icon('arrow-down'), h('strong', {}, String(repo.behind)), ' to pull'),
        ],
    ),
    h('div', { class: 'repository-section' },
      h('strong', { class: 'repository-label' }, 'Working tree'),
      h('div', { class: 'repository-changes' }, changes.length ? changes : h('span', { class: 'repository-clean' }, icon('check'), 'Nothing to commit')),
    ),
    h('div', { class: 'repository-section repository-footer' },
      h('p', {}, icon('sync'), 'Last fetch: ', repo.fetchedAt
        ? h('time', { datetime: repo.fetchedAt }, ago(repo.fetchedAt))
        : 'Never fetched'),
      repo.fetchError && h('p', { class: 'repository-error' }, icon('warning'), `Fetch failed: ${repo.fetchError}`),
      h('code', { class: 'repository-directory' }, repo.directory),
    ),
  );
}

function fact(label: string, value: HTMLElement | string): HTMLElement {
  return h('div', {}, h('dt', {}, label), h('dd', {}, value));
}
