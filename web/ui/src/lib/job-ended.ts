/**
 * The end of a Job, told to whoever on the page wants to know.
 *
 * The stream says it as it happens; the demo, which has no stream, says it
 * itself. Whoever listens decides whether it is worth interrupting the person.
 */
export interface JobEnded {
  sessionId: string;
  failed: boolean;
  /** The summary of a finished Job, or why it failed. */
  text: string;
}

type Listener = (ended: JobEnded) => void;

const listeners = new Set<Listener>();

export function announceJobEnded(ended: JobEnded) {
  for (const listener of listeners) listener(ended);
}

export function onJobEnded(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
