import { expect, it, vi } from 'vitest';
import type { BackendInstance, Event } from '../src/api/types';
import { IDS } from '../../../web/ui/src/demo/data';
import { demoBackendLifecycle, demoRequest } from '../../../web/ui/src/demo/server';

it('plays backend transitions, changes the data clients read and stops on cleanup', async () => {
  vi.useFakeTimers();
  const read = async () => {
    const request = demoRequest('GET', '/api/v1/backends', null);
    await vi.advanceTimersByTimeAsync(60);
    return (await request as { items: BackendInstance[] }).items.find((backend) => backend.id === IDS.ci)!;
  };
  const events: Event[] = [];
  const before = await read();
  const stop = demoBackendLifecycle((event) => events.push(event));
  try {
    await vi.advanceTimersByTimeAsync(8_000);
    expect((await read()).operationalStatus).toBe('OFFLINE');
    await vi.advanceTimersByTimeAsync(8_000);
    expect((await read()).operationalStatus).toBe('STARTING');
    await vi.advanceTimersByTimeAsync(8_000);
    const ready = await read();
    expect(ready.operationalStatus).toBe('READY');
    expect(ready.providerAuthState).toBe('AUTHENTICATED');
    expect(ready.conditions).toEqual([]);
    expect(events.map((event) => event.payload?.operationalStatus)).toEqual(['OFFLINE', 'STARTING', 'READY']);
    expect(events.every((event) => !event.sessionId && event.type === 'backend.status_changed' && event.payload?.backendInstanceId === IDS.ci)).toBe(true);
    const readAudit = async () => {
      const request = demoRequest('GET', '/api/v1/me/audit', null);
      await vi.advanceTimersByTimeAsync(60);
      return (await request as { items: { action: string; subjectId?: string; detail?: { to?: string } }[] }).items
        .filter((entry) => entry.action === 'backend.availability_changed' && entry.subjectId === IDS.ci);
    };
    expect((await readAudit()).map((entry) => entry.detail?.to)).toEqual(['READY', 'OFFLINE']);
    stop();
    await vi.advanceTimersByTimeAsync(32_000);
    expect(events).toHaveLength(3);
    expect((await read()).operationalStatus).toBe(before.operationalStatus);
    expect(await readAudit()).toEqual([]);
  } finally {
    stop();
    vi.useRealTimers();
  }
});
