import { describe, expect, it } from 'vitest';
import { reviewLines, reviewTree, splitReviewLines, wholeFileLines } from '../../shared/review';
import { demoReview, demoReviewAction, demoReviewFile } from '../../../web/ui/src/demo/review';

describe('Review source positions', () => {
  it('keeps real line numbers across distant hunks and removed lines', () => {
    const lines = reviewLines('diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -7,2 +7,3 @@\n old\n-removed\n+first\n+second\n@@ -90 +91 @@\n-final\n+replacement\n');
    expect(lines.filter((line) => line.kind === 'deleted').map((line) => line.before)).toEqual([8, 90]);
    expect(lines.filter((line) => line.kind === 'added').map((line) => line.after)).toEqual([8, 9, 91]);
    expect(splitReviewLines(lines).filter((line) => line.before?.kind === 'deleted').at(-1)?.after?.after).toBe(91);
  });
  it('shows full-file line positions independently of diff reconstruction', () => {
    expect(wholeFileLines('first\nsecond\n', 'after').map((line) => line.after)).toEqual([1, 2]);
    expect(wholeFileLines('', 'before')).toEqual([]);
  });
  it('builds an actual directory tree with duplicate basenames', () => {
    const tree = reviewTree([{ path: 'app/config.ts', state: 'MODIFIED' }, { path: 'worker/config.ts', state: 'ADDED' }]);
    expect(tree.map((node) => node.name)).toEqual(['app', 'worker']);
    expect(tree[1].children[0].file?.path).toBe('worker/config.ts');
  });
});

describe('Usable human-review demo', () => {
  it('saves drafts, sends one batch, keeps earlier code and resolves before approval', () => {
    const session = crypto.randomUUID();
    let review = structuredClone(demoReview(session)); const revision = review.revisions[0], repo = review.repositories[0];
    const act = (input: Parameters<typeof demoReviewAction>[1]) => { review = structuredClone(demoReviewAction(session, { ...input, expectedVersion: review.version })); };
    act({ action: 'require', expectedVersion: review.version });
    act({ action: 'comment', expectedVersion: review.version, revisionId: revision.id, repositoryId: repo.id, path: 'apps/ingress/values.yaml', side: 'after', startLine: 1, endLine: 1, body: 'Use three replicas' });
    expect(review.threads[0].messages[0].draft).toBe(true);
    expect(review.threads[0].excerpt).toBe('replicaCount: 2');
    expect(() => demoReviewAction(session, { action: 'approve', expectedVersion: review.version, revisionId: revision.id })).toThrow();
    const batch = crypto.randomUUID(); act({ action: 'publish', expectedVersion: review.version, revisionId: revision.id, batchId: batch });
    const count = review.revisions.length;
    act({ action: 'publish', expectedVersion: 0, revisionId: revision.id, batchId: batch });
    expect(review.revisions).toHaveLength(count);
    expect(review.threads[0].messages[0].draft).toBe(false);
    expect(review.required).toBe(true);
    const oldFile = demoReviewFile(session, new URLSearchParams({ revisionId: revision.id, repositoryId: repo.id, path: 'apps/ingress/values.yaml' }));
    expect(oldFile.after).toContain('replicaCount: 2');
    act({ action: 'resolve', expectedVersion: review.version, threadId: review.threads[0].id, resolved: true });
    act({ action: 'approve', expectedVersion: review.version, revisionId: review.revisions.at(-1)!.id });
    expect(review.status).toBe('APPROVED'); expect(review.required).toBe(false);
  });
});
