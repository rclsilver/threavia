import { CHANNEL, type CoreClient } from './client';
import type { Event } from './types';

/**
 * The global event stream, with the same semantics as the web client's
 * (web/ui/src/api/stream.ts).
 *
 * One stream per user carries every Session, so the extension holds a single
 * cursor: the global sequence of the last persisted event it applied. Core
 * replays everything after `after=<cursor>` on a reconnection, so a dropped
 * connection costs nothing. Unlike a browser tab, the extension keeps the
 * cursor across restarts, so opening the editor in the morning replays the
 * night rather than the whole history.
 */

/** One frame, as SSE delimits it. */
export interface Frame {
  name: string;
  data: string;
  id?: string;
}

/**
 * Cuts a byte stream into frames.
 *
 * Pure and incremental: chunks arrive split anywhere, including in the middle
 * of a line or of a multi-byte character, and the parser holds what is still
 * arriving.
 */
export class FrameParser {
  private buffer = '';
  private readonly decoder = new TextDecoder();

  /** Feeds bytes or text, and returns the frames that are now complete. */
  push(chunk: Uint8Array | string): Frame[] {
    // Decoded with `stream: true` so a character split across two chunks is
    // held until the rest of it arrives.
    this.buffer += typeof chunk === 'string' ? chunk : this.decoder.decode(chunk, { stream: true });
    // The spec allows CRLF and CR line ends; Core writes LF, a proxy may not.
    this.buffer = this.buffer.replace(/\r\n?/g, '\n');

    const frames: Frame[] = [];
    // A frame ends at a blank line. Anything after the last one is a frame
    // still arriving, so it stays in the buffer.
    let boundary = this.buffer.indexOf('\n\n');
    while (boundary !== -1) {
      const frame = parseFrame(this.buffer.slice(0, boundary));
      if (frame) frames.push(frame);
      this.buffer = this.buffer.slice(boundary + 2);
      boundary = this.buffer.indexOf('\n\n');
    }
    return frames;
  }
}

/**
 * Reads one frame. Core names them, so there is no default `message`: `event`
 * carries the persisted timeline, `ephemeral` the liveness signals that are
 * streamed and never stored. A comment line is a keep-alive.
 */
export function parseFrame(raw: string): Frame | null {
  let name = 'message';
  let data = '';
  let id: string | undefined;

  for (const line of raw.split('\n')) {
    if (line.startsWith(':')) continue;
    const colon = line.indexOf(':');
    const field = colon === -1 ? line : line.slice(0, colon);
    const value = colon === -1 ? '' : line.slice(colon + 1).replace(/^ /, '');

    if (field === 'event') name = value;
    if (field === 'id') id = value;
    // A data field may be repeated; the spec joins them with a newline.
    if (field === 'data') data = data ? `${data}\n${value}` : value;
  }
  if (!data) return null;
  return { name, data, id };
}

/** A liveness signal: the agent of a Session is still working. Never stored. */
export interface Activity {
  sessionId: string;
  kind: string;
  at: number;
}

export interface StreamHandlers {
  /** A persisted event, in order, at most once per sequence. */
  onEvent: (event: Event) => void;
  onActivity?: (activity: Activity) => void;
  onConnection?: (connected: boolean) => void;
  /** The cursor moved; worth saving so the next start resumes from it. */
  onCursor?: (cursor: number) => void;
  /** Core refused the credential: retrying alone will not fix it. */
  onUnauthorized?: () => void;
}

export interface StreamOptions {
  client: CoreClient;
  /** Whether the person is looking at this editor, sent when the stream opens. */
  isActive: () => boolean;
  /** Told the presence the stream declared, so it is not reported twice. */
  presenceSent?: (active: boolean) => void;
  initialCursor?: number;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
  now?: () => number;
}

export class EventStream {
  private abort: AbortController | null = null;
  private cursor: number;
  private readonly options: StreamOptions;

  constructor(options: StreamOptions) {
    this.options = options;
    this.cursor = options.initialCursor ?? 0;
  }

  /** The sequence reached so far, which a reconnection resumes from. */
  get position() {
    return this.cursor;
  }

  /** The query a (re)connection asks with: everything after the cursor. */
  path(): string {
    return `/api/v1/events?after=${this.cursor}&channel=${CHANNEL}`;
  }

  /**
   * Opens the stream and keeps it open, reconnecting with a backoff.
   *
   * Read with fetch rather than an EventSource, which cannot send the bearer
   * token. The resume an EventSource would do with Last-Event-ID is the
   * cursor, sent as `after`.
   */
  open(handlers: StreamHandlers) {
    this.close();
    const abort = new AbortController();
    this.abort = abort;
    void this.run(abort, handlers);
  }

  close() {
    this.abort?.abort();
    this.abort = null;
  }

  get isOpen() {
    return this.abort !== null;
  }

  private async run(abort: AbortController, handlers: StreamHandlers) {
    const sleep = this.options.sleep ?? defaultSleep;
    // Backs off so a Core that is down is not hammered, and recovers quickly
    // when it is a blip.
    let backoff = 1000;

    while (!abort.signal.aborted) {
      let refused = false;
      try {
        // The stream says whether the person is looking, so Core never holds
        // a fresh connection as present by mistake until the first change.
        const active = this.options.isActive();
        const response = await this.options.client.raw(this.path(), {
          headers: { Accept: 'text/event-stream', 'X-Threavia-Active': String(active) },
          signal: abort.signal,
        });
        if (response.status === 401) {
          refused = true;
          throw new Error('the stream was refused');
        }
        if (!response.ok || !response.body) {
          throw new Error(`the stream did not open (${response.status})`);
        }
        this.options.presenceSent?.(active);

        handlers.onConnection?.(true);
        backoff = 1000;
        await this.consume(response.body, abort.signal, handlers);
      } catch {
        // An abort is the caller closing the stream, not a failure.
        if (abort.signal.aborted) return;
      }

      handlers.onConnection?.(false);
      if (abort.signal.aborted) return;
      if (refused) {
        // Hammering Core with a credential it refused only fills its logs;
        // the auth layer reopens the stream once there is a new one.
        handlers.onUnauthorized?.();
        this.abort = null;
        return;
      }
      await sleep(backoff, abort.signal);
      backoff = Math.min(backoff * 2, 30_000);
    }
  }

  /** Reads frames until the connection ends. */
  private async consume(body: ReadableStream<Uint8Array>, signal: AbortSignal, handlers: StreamHandlers) {
    const reader = body.getReader();
    const parser = new FrameParser();
    try {
      while (!signal.aborted) {
        const { done, value } = await reader.read();
        if (done) return;
        for (const frame of parser.push(value)) this.route(frame, handlers);
      }
    } finally {
      reader.releaseLock();
    }
  }

  /**
   * Routes one frame by its name.
   *
   * The cursor only ever moves here, from the events themselves. A Session
   * snapshot's cursor is not fed back into it: the sequence is global, so
   * raising the cursor to a snapshot's would skip events of other Sessions
   * that the catch-up had not delivered yet.
   */
  private route(frame: Frame, handlers: StreamHandlers) {
    let payload: Event;
    try {
      payload = JSON.parse(frame.data) as Event;
    } catch {
      return;
    }

    if (frame.name === 'event') {
      // A reconnection replays from the cursor and the catch-up may overlap
      // the live tail, so the same event can arrive twice. The sequence is
      // the identity Core assigned it.
      if (payload.sequence <= this.cursor) return;
      this.cursor = payload.sequence;
      handlers.onEvent(payload);
      handlers.onCursor?.(this.cursor);
      return;
    }
    if (frame.name === 'ephemeral' && payload.sessionId) {
      handlers.onActivity?.({
        sessionId: payload.sessionId,
        kind: payload.type,
        at: (this.options.now ?? Date.now)(),
      });
    }
  }

  /** Applies frames as if they had arrived on the wire. For tests. */
  deliver(frames: Frame[], handlers: StreamHandlers) {
    for (const frame of frames) this.route(frame, handlers);
  }
}

/** A delay that gives up when the stream is closed under it. */
function defaultSleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener(
      'abort',
      () => {
        clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
  });
}
