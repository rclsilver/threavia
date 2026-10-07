import type { QueryClient } from '@tanstack/react-query';

import { keys } from './keys';
import type { Snapshot } from './types';

/**
 * Drops a resolved request from the Session that is showing it.
 *
 * A Session carries its own copy of the pending attention in the snapshot it
 * was opened with, so clearing the standalone list is not enough: the card the
 * user is looking at lives in the snapshot.
 *
 * Both the stream and the resolving mutation call this. The stream is the one
 * that matters — it is what makes a request answered on another device
 * disappear here — and the mutation covers the case where this client answered
 * while its own stream was down.
 */
export function dropResolvedAttention(
  queries: QueryClient,
  sessionId: string | undefined,
  ids: { validationId?: string; userInputId?: string },
) {
  if (!sessionId) return;

  queries.setQueryData<Snapshot>(keys.snapshot(sessionId), (snapshot) => {
    if (!snapshot) return snapshot;

    const validations = (snapshot.attention.validations ?? []).filter(
      (item) => item.id !== ids.validationId,
    );
    const userInputs = (snapshot.attention.userInputs ?? []).filter(
      (item) => item.id !== ids.userInputId,
    );

    // Nothing matched, so nothing changed: returning the same object keeps the
    // views that read it from re-rendering for no reason.
    if (
      validations.length === (snapshot.attention.validations ?? []).length &&
      userInputs.length === (snapshot.attention.userInputs ?? []).length
    ) {
      return snapshot;
    }
    return { ...snapshot, attention: { validations, userInputs } };
  });
}
