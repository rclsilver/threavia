import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';

import { EventStream, type Activity } from '@/api/stream';
import { followPresence } from '@/lib/presence';
import { StreamContext } from '@/use-stream';

/** How long a liveness signal keeps saying the agent is working. */
const ACTIVITY_TTL = 15_000;

/**
 * Holds the one event stream the client has.
 *
 * One stream per user carries every Session, so this sits above the router: a
 * navigation must not drop the connection and replay history to get it back.
 */
export function StreamProvider({ children }: { children: ReactNode }) {
  const queries = useQueryClient();
  const [connected, setConnected] = useState(false);
  const [activity, setActivity] = useState<Record<string, Activity>>({});
  const stream = useRef<EventStream | null>(null);

  stream.current ??= new EventStream(queries);

  useEffect(() => {
    const live = stream.current!;
    followPresence();
    live.open(setConnected, (signal) => {
      setActivity((current) => ({ ...current, [signal.sessionId]: signal }));
    });
    return () => live.close();
  }, []);

  // A signal is liveness, so it expires rather than lingering as a stale claim
  // that something is still running.
  useEffect(() => {
    const timer = setInterval(() => {
      setActivity((current) => {
        const fresh = Object.fromEntries(
          Object.entries(current).filter(([, signal]) => Date.now() - signal.at < ACTIVITY_TTL),
        );
        return Object.keys(fresh).length === Object.keys(current).length ? current : fresh;
      });
    }, 5_000);
    return () => clearInterval(timer);
  }, []);

  const lastSeen = useCallback(
    (sessionId: string) => {
      const signal = activity[sessionId];
      if (!signal || Date.now() - signal.at >= ACTIVITY_TTL) return null;
      return signal.kind;
    },
    [activity],
  );

  return (
    <StreamContext
      value={{
        connected,
        seen: (sequence) => stream.current?.seen(sequence),
        activity: lastSeen,
      }}
    >
      {children}
    </StreamContext>
  );
}
