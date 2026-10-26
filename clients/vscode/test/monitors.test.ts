import { describe, it, expect } from 'vitest';
import { effectsOf } from '../src/api/events';
import { demoRequest } from '../../../web/ui/src/demo/server';
import { IDS } from '../../../web/ui/src/demo/data';
import type { Monitor } from '../src/api/types';

describe('persistent monitoring', () => {
 it('refreshes session indicators on monitor changes', () => {
  expect(effectsOf({id:'m',sequence:1,timestamp:new Date().toISOString(),type:'monitor.changed',sessionId:'s'})).toEqual([{kind:'sessions'}]);
 });
 it('plays the same monitor and its stop in session and project views', async () => {
  const path = `/api/v1/sessions/${IDS.grafana}/monitors`;
  const list = await demoRequest('GET', path, undefined) as {items:Monitor[]};
  expect(list.items).toHaveLength(1);
  expect(list.items[0].state).toBe('RUNNING');
  await demoRequest('POST', `/api/v1/monitors/${list.items[0].id}/stop`, {});
  const project = await demoRequest('GET', `/api/v1/projects/${IDS.homelab}/monitors`, undefined) as {items:Monitor[]};
  expect(project.items.find(m => m.id === list.items[0].id)?.state).toBe('STOPPED');
 });
});
