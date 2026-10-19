import { createContext, use } from 'react';

export interface StreamState {
  connected: boolean;
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
  activity: () => null,
});

/** What the global event stream is currently doing. */
export function useStream() {
  return use(StreamContext);
}
