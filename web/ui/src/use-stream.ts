import { createContext, use } from 'react';

export interface StreamState {
  connected: boolean;
  /** Raises the cursor from a snapshot, so a reconnection does not rewind. */
  seen: (sequence: number) => void;
  /**
   * What the agent was last seen doing in a Session, if anything recently.
   *
   * A liveness signal, never history: it says the agent is still working
   * between two things worth remembering, and it goes quiet on its own.
   */
  activity: (sessionId: string) => string | null;
}

export const StreamContext = createContext<StreamState>({
  connected: false,
  seen: () => {},
  activity: () => null,
});

/** What the global event stream is currently doing. */
export function useStream() {
  return use(StreamContext);
}
