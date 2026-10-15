import { expect, it } from 'vitest';

import { EventStream } from '../../../web/ui/src/api/stream';
import { EventBus } from '../src/api/events';
import type { Event } from '../src/api/types';

it('refreshes backend, session and attention state in both clients on a global lifecycle event', () => {
  const invalidated: (readonly unknown[])[] = [];
  const queries = { invalidateQueries: ({ queryKey }: { queryKey: readonly unknown[] }) => {
    invalidated.push(queryKey);
    return Promise.resolve();
  } } as unknown as ConstructorParameters<typeof EventStream>[0];
  const web = new EventStream(queries);
  const editor = new EventBus();
  const effects: string[] = [];
  editor.on('effect', (effect) => effects.push(effect.kind));
  const event: Event = { id: 'state', type: 'backend.status_changed', sequence: 17, timestamp: new Date().toISOString(),
    payload: { backendInstanceId: 'backend', operationalStatus: 'OFFLINE', providerAuthState: 'AUTHENTICATED' } };
  web.deliver(event);
  editor.publish(event);
  expect(web.position).toBe(17);
  expect(effects).toEqual(['backends', 'sessions', 'attention']);
  expect(invalidated).toEqual([['audit'], ['backends'], ['sessions'], ['snapshot'], ['attention']]);
});
