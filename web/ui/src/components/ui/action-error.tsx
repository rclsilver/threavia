/**
 * Why an action did not go through, next to the control that tried it.
 *
 * Every mutation the person starts says when it fails: a message, an approval
 * or a rule that silently did not land is worse than one that visibly failed.
 * It says the outcome first ("Not approved"), then Core's own reason, then
 * what happens now — a reason alone leaves the person to guess whether
 * anything changed. Announced as an alert, so it is heard as well as seen.
 */
export function ActionError({
  error,
  outcome,
  recovery,
  className,
}: {
  error: Error | null | undefined;
  /** What did not happen, e.g. "Not approved". */
  outcome?: string;
  /** What the person can do now, e.g. "It is still waiting; try again once it reconnects." */
  recovery?: string;
  className?: string;
}) {
  if (!error) return null;
  const reason = error.message || 'that did not go through';
  const sentence = reason.charAt(0).toUpperCase() + reason.slice(1);
  return (
    <p role="alert" className={['text-danger text-sm', className].filter(Boolean).join(' ')}>
      {outcome ? `${outcome}: ${reason}.` : `${sentence.replace(/\.$/, '')}.`}
      {recovery ? ` ${recovery}` : ' Try again.'}
    </p>
  );
}
