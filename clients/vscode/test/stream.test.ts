import { describe, expect, it } from 'vitest';

import { CoreClient } from '../src/api/client';
import { EventStream, FrameParser, parseFrame } from '../src/api/stream';
import type { Event } from '../src/api/types';

const event = (sequence: number, type = 'agent.message'): Event => ({
  id: `e${sequence}`,
  sequence,
  timestamp: new Date(0).toISOString(),
  type,
  sessionId: 's1',
});

const frame = (value: Event) => `id: ${value.sequence}\nevent: event\ndata: ${JSON.stringify(value)}\n\n`;

describe('the frame parser', () => {
  it('holds a frame until its blank line arrives, wherever the chunks are cut', () => {
    const parser = new FrameParser();
    const text = frame(event(1)) + ': keepalive\n\n' + frame(event(2));
    const frames = [];
    for (let i = 0; i < text.length; i += 7) frames.push(...parser.push(text.slice(i, i + 7)));
    expect(frames.map((f) => [f.name, f.id])).toEqual([
      ['event', '1'],
      ['event', '2'],
    ]);
  });

  it('keeps a multi-byte character split across two chunks', () => {
    const parser = new FrameParser();
    const bytes = new TextEncoder().encode('event: event\ndata: {"t":"é"}\n\n');
    const cut = bytes.indexOf(0xc3) + 1;
    expect(parser.push(bytes.slice(0, cut))).toEqual([]);
    expect(parser.push(bytes.slice(cut))[0].data).toBe('{"t":"é"}');
  });

  it('joins repeated data lines, reads CRLF, and skips comments and empty frames', () => {
    expect(parseFrame('event: x\ndata: a\ndata: b')).toEqual({ name: 'x', data: 'a\nb', id: undefined });
    expect(parseFrame(': keepalive')).toBeNull();
    expect(new FrameParser().push('event: e\r\ndata: 1\r\n\r\n')).toEqual([{ name: 'e', data: '1', id: undefined }]);
  });
});

/** A fake Core: each connection answers with the next scripted body. */
function fakeCore(bodies: string[]) {
  const urls: string[] = [];
  const headers: Headers[] = [];
  const fetcher = ((input: string, init?: RequestInit) => {
    urls.push(input);
    headers.push(new Headers(init?.headers));
    const body = bodies.shift();
    if (body === undefined) {
      // No more scripted connections: hang until the stream is closed.
      return new Promise<Response>((_resolve, reject) =>
        init?.signal?.addEventListener('abort', () => reject(new Error('aborted'))),
      );
    }
    return Promise.resolve(new Response(body, { status: 200 }));
  }) as typeof fetch;
  const client = new CoreClient({
    baseUrl: () => 'http://core',
    identity: { clientId: 'id-1', clientName: () => 'VS Code — laptop' },
    authorization: () => Promise.resolve('Bearer t'),
    fetch: fetcher,
  });
  return { client, urls, headers };
}

describe('the event stream', () => {
  it('resumes after the last event it applied, and applies each event once', async () => {
    const core = fakeCore([frame(event(1)) + frame(event(2)), frame(event(2)) + frame(event(3))]);
    const seen: number[] = [];
    const cursors: number[] = [];
    const stream = new EventStream({
      client: core.client,
      isActive: () => true,
      initialCursor: 0,
      sleep: () => Promise.resolve(),
    });

    await new Promise<void>((done) => {
      stream.open({
        onEvent: (value) => {
          seen.push(value.sequence);
          if (value.sequence === 3) done();
        },
        onCursor: (cursor) => cursors.push(cursor),
      });
    });
    stream.close();

    expect(seen).toEqual([1, 2, 3]);
    expect(cursors.at(-1)).toBe(3);
    expect(core.urls[0]).toBe('http://core/api/v1/events?after=0&channel=vscode');
    expect(core.urls[1]).toBe('http://core/api/v1/events?after=2&channel=vscode');
  });

  it('starts from a saved cursor, and says who is asking and whether they are looking', async () => {
    const core = fakeCore([frame(event(42))]);
    const stream = new EventStream({ client: core.client, isActive: () => false, initialCursor: 41 });
    await new Promise<void>((done) => {
      stream.open({ onEvent: () => done() });
    });
    stream.close();
    expect(core.urls[0]).toContain('after=41');
    const sent = core.headers[0];
    expect(sent.get('X-Threavia-Active')).toBe('false');
    expect(sent.get('X-Threavia-Channel')).toBe('vscode');
    expect(sent.get('X-Threavia-Client')).toBe('id-1');
    expect(decodeURIComponent(sent.get('X-Threavia-Client-Name') ?? '')).toBe('VS Code — laptop');
    expect(sent.get('Authorization')).toBe('Bearer t');
  });

  it('passes liveness signals on without moving the cursor', () => {
    const core = fakeCore([]);
    const stream = new EventStream({ client: core.client, isActive: () => true, initialCursor: 5, now: () => 7 });
    const activity: unknown[] = [];
    stream.deliver(
      [{ name: 'ephemeral', data: JSON.stringify({ ...event(0, 'agent.thinking') }) }],
      { onEvent: () => {}, onActivity: (signal) => activity.push(signal) },
    );
    expect(activity).toEqual([{ sessionId: 's1', kind: 'agent.thinking', at: 7 }]);
    expect(stream.position).toBe(5);
  });

  it('stops retrying when Core refuses the credential', async () => {
    const fetcher = (() => Promise.resolve(new Response('{}', { status: 401 }))) as typeof fetch;
    const client = new CoreClient({
      baseUrl: () => 'http://core',
      identity: { clientId: 'id', clientName: () => 'n' },
      authorization: () => Promise.resolve(undefined),
      fetch: fetcher,
    });
    const stream = new EventStream({ client, isActive: () => true, sleep: () => Promise.resolve() });
    await new Promise<void>((done) => stream.open({ onEvent: () => {}, onUnauthorized: done }));
    expect(stream.isOpen).toBe(false);
  });
});
