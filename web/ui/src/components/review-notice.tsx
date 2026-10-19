import { Link } from '@tanstack/react-router';
import { useReview } from '@/api/reviews';

export function ReviewNotice({ sessionId }: { sessionId: string }) {
  const review = useReview(sessionId);
  if (!review.data?.required) return null;
  return <p className="border-b border-border bg-warn/10 px-4 py-3 text-sm text-warn-text" role="status">
    {review.data.status === 'CORRECTING' ? 'Review corrections are in progress. Your validation is required afterwards. ' : 'Human review required. Queued work is blocked; a connected agent pauses before its next action. '}
    <Link className="font-medium underline underline-offset-4" to="/sessions/$sessionId/review" params={{ sessionId }}>Open code review</Link>
  </p>;
}
