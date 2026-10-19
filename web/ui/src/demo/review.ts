import { ApiError } from '@/api/client';
import { uuid } from '@/lib/uuid';
import type { Review, ReviewAction, ReviewFileContent } from '@/api/reviews';

const reviews = new Map<string, Review>();
const contents = new Map<string, Record<string, { before: string; after: string }>>();
const id = uuid;
const now = () => new Date().toISOString();
const conflict = (message: string) => new ApiError(409, 'conflict', message);

export function demoReview(sessionId: string): Review {
  const existing = reviews.get(sessionId); if (existing) return existing;
  const first = id(), second = id(), revision = id();
  const review: Review = { sessionId, version: 1, required: false, status: 'OPEN', updatedAt: now(), seen: {},
    repositories: [
      { id: first, backendInstanceId: 'demo-backend', name: 'homelab', directory: '/home/alex/git/homelab-review', gitDirectory: '/home/alex/git/homelab/.git', initialTree: 'base-config', approvedTree: 'base-config', baselineSource: 'BEFORE_WORK' },
      { id: second, backendInstanceId: 'demo-backend', name: 'status-page', directory: '/home/alex/git/status-page-review', gitDirectory: '/home/alex/git/status-page/.git', initialTree: 'base-page', approvedTree: 'base-page', baselineSource: 'BEFORE_WORK' },
    ], revisions: [{ id: revision, createdAt: now(), approved: false, repositories: [
      { repositoryId: first, baseTree: 'base-config', headTree: 'config-v1', files: [{ path: 'apps/ingress/values.yaml', state: 'MODIFIED' }, { path: 'README.md', state: 'MODIFIED' }] },
      { repositoryId: second, baseTree: 'base-page', headTree: 'page-v1', files: [{ path: 'src/health.ts', state: 'ADDED' }] },
    ] }], threads: [] };
  contents.set(revision, {
    [`${first}:apps/ingress/values.yaml`]: { before: 'replicaCount: 1\nservice:\n  type: ClusterIP\n  port: 80\n', after: 'replicaCount: 2\nservice:\n  type: ClusterIP\n  port: 8080\n' },
    [`${first}:README.md`]: { before: '# Homelab\n\nServices run on the home cluster.\n', after: '# Homelab\n\nServices run on the home cluster.\nThe ingress now listens on port 8080.\n' },
    [`${second}:src/health.ts`]: { before: '', after: 'export async function healthy(url: string): Promise<boolean> {\n  const response = await fetch(url);\n  return response.ok;\n}\n' },
  });
  reviews.set(sessionId, review); return review;
}

function diff(before: string, after: string, path: string): string {
  const lines = (value: string) => value ? value.replace(/\n$/, '').split('\n') : [];
  const a = lines(before), b = lines(after);
  if (before === after) return '';
  return `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -${a.length ? 1 : 0},${a.length} +${b.length ? 1 : 0},${b.length} @@\n${a.map((line) => `-${line}\n`).join('')}${b.map((line) => `+${line}\n`).join('')}`;
}

export function demoReviewFile(sessionId: string, query: URLSearchParams): ReviewFileContent {
  const review = demoReview(sessionId), revision = query.get('revisionId') ?? '', repo = query.get('repositoryId') ?? '', path = query.get('path') ?? '';
  const value = contents.get(revision)?.[`${repo}:${path}`]; if (!value) throw new ApiError(404, 'not_found', 'This file is not in the captured revision.');
  let before = value.before;
  const compare = query.get('compareTo'); if (compare && compare !== 'initial') before = contents.get(compare)?.[`${repo}:${path}`]?.after ?? '';
  if (!review.repositories.some((entry) => entry.id === repo)) throw new ApiError(404, 'not_found', 'Repository not found.');
  return { path, before, after: value.after, diff: diff(before, value.after, path), binary: false, truncated: false };
}

export function demoReviewAction(sessionId: string, input: ReviewAction): Review {
  const review = demoReview(sessionId);
  if (input.action === 'publish' && review.threads.some((thread) => thread.messages.some((message) => message.batchId === input.batchId))) return review;
  if (input.expectedVersion !== review.version) throw conflict('The review changed on another client. Reload before retrying.');
  const thread = review.threads.find((item) => item.id === input.threadId);
  switch (input.action) {
    case 'refresh': break;
    case 'register': throw new ApiError(503, 'demo', 'The browser demo cannot read repositories on your machine.');
    case 'require': review.required = true; review.status = 'WAITING'; break;
    case 'approve':
      if (review.threads.some((item) => !item.resolved || item.messages.some((message) => message.draft))) throw conflict('Resolve discussions and send or discard drafts before approving.');
      if (input.revisionId !== review.revisions.at(-1)?.id) throw conflict('Approve the latest revision.');
      review.revisions.at(-1)!.approved = true; review.status = 'APPROVED'; review.required = false; break;
    case 'comment': {
      if (!input.body?.trim()) throw new ApiError(400, 'invalid', 'Write a comment before saving.');
      const message = { id: id(), body: input.body, author: 'user', draft: true, createdAt: now() };
      if (thread) { thread.messages.push(message); thread.resolved = false; }
      else {
        const target = { id: id(), revisionId: input.revisionId ?? '', repositoryId: input.repositoryId, path: input.path, side: input.side, startLine: input.startLine, endLine: input.endLine ?? input.startLine, resolved: false, messages: [message], excerpt: '' };
        if (input.path && input.startLine) { const text = demoReviewFile(sessionId, new URLSearchParams({ revisionId: input.revisionId ?? '', repositoryId: input.repositoryId ?? '', path: input.path })); target.excerpt = text[input.side ?? 'after'].split('\n').slice(input.startLine - 1, input.endLine ?? input.startLine).join('\n'); }
        review.threads.push(target);
      }
      break;
    }
    case 'edit': case 'delete': {
      const index = thread?.messages.findIndex((message) => message.id === input.messageId) ?? -1;
      if (!thread || index < 0) throw new ApiError(404, 'not_found', 'Draft not found.');
      if (!thread.messages[index].draft) throw conflict('Published comments are immutable.');
      if (input.action === 'edit') thread.messages[index].body = input.body ?? ''; else { thread.messages.splice(index, 1); if (!thread.messages.length) review.threads.splice(review.threads.indexOf(thread), 1); }
      break;
    }
    case 'resolve': if (!thread) throw new ApiError(404, 'not_found', 'Discussion not found.'); thread.resolved = input.resolved ?? false; break;
    case 'seen': { const tree = review.revisions.find((item) => item.id === input.revisionId)?.repositories.find((item) => item.repositoryId === input.repositoryId)?.headTree; if (tree) review.seen[`${input.repositoryId}:${input.path}`] = tree; break; }
    case 'publish': {
      if (!review.threads.some((item) => item.messages.some((message) => message.draft))) throw conflict('Add a draft comment first.');
      for (const item of review.threads) { let sent = false; for (const message of item.messages) if (message.draft) { message.draft = false; message.batchId = input.batchId; sent = true; } if (sent) item.messages.push({ id: id(), body: 'Demo agent: the requested changes are ready for another human review.', author: 'agent', draft: false, createdAt: now() }); }
      const previous = review.revisions.at(-1)!; const revision = structuredClone(previous); revision.id = id(); revision.createdAt = now(); revision.approved = false;
      const values = structuredClone(contents.get(previous.id)!); for (const value of Object.values(values)) value.after = value.after.replace('replicaCount: 2', 'replicaCount: 3');
      contents.set(revision.id, values); revision.repositories.forEach((repo) => { repo.headTree += '-corrected'; }); review.revisions.push(revision); review.status = review.required ? 'WAITING' : 'OPEN'; break;
    }
  }
  review.version++; review.updatedAt = now(); return review;
}
