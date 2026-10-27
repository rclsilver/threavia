import { expect, it } from 'vitest';
import { learningPrompt } from '../../shared/learnings';
import { demoRequest } from '../../../web/ui/src/demo/server';
import { IDS } from '../../../web/ui/src/demo/data';
import type { Snapshot, Decision } from '../../../web/ui/src/api/types';

async function start() {
  const created = await demoRequest('POST', '/api/v1/sessions/start', { projectId: IDS.homelab, backendInstanceId: IDS.laptop, message: 'Verified example' }) as { session: { id: string } };
  const path = `/api/v1/sessions/${created.session.id}`;
  const snapshot = await demoRequest('GET', path, null) as Snapshot;
  const source = snapshot.jobs.at(-1)!.id;
  return { path, source };
}
async function memory() {
  return (await demoRequest('GET', `/api/v1/projects/${IDS.homelab}/decisions`, null) as { items: Decision[] }).items;
}
async function question(path: string) {
  const snapshot = await demoRequest('GET', path, null) as Snapshot;
  return snapshot.attention.userInputs![0];
}
const answer = (id: string, value: string) => demoRequest('POST', `/api/v1/user-input/${id}/resolve`, { value });

it('plays explicit review, editing, confirmation and a real demo-memory save', async () => {
  const { path, source } = await start();
  const before = await memory();
  await demoRequest('POST', `${path}/messages`, { message: learningPrompt(source), afterJobId: source });
  expect(await memory()).toEqual(before);
  expect((await question(path)).choices).toEqual(['Save', 'Edit', 'Ignore']);
  await answer((await question(path)).id, 'Edit');
  await answer((await question(path)).id, 'Keep only the verified result and its source.');
  const revised = await question(path);
  expect(revised.prompt).toContain('Keep only the verified result and its source.');
  expect(await memory()).toEqual(before);
  await answer(revised.id, 'Save');
  expect(await memory()).toEqual(expect.arrayContaining([expect.objectContaining({ content: 'Keep only the verified result and its source.', importance: 'NORMAL', projectId: IDS.homelab })]));
  expect(await question(path)).toBeUndefined();
});

it('leaves memory untouched on Ignore and refuses a stale source', async () => {
  const { path, source } = await start();
  const before = await memory();
  await demoRequest('POST', `${path}/messages`, { message: learningPrompt(source), afterJobId: source });
  await expect(demoRequest('POST', `${path}/messages`, { message: learningPrompt(source), afterJobId: source })).rejects.toMatchObject({ status: 409 });
  await answer((await question(path)).id, 'Ignore');
  expect(await memory()).toEqual(before);
});
