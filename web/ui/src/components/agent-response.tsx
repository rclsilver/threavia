import { Check, Copy } from 'lucide-react';
import { useEffect, useState } from 'react';

import { Markdown } from '@/components/markdown';
import { Button } from '@/components/ui/button';

/** Copy the source, so Markdown survives pasting into another application. */
export function AgentResponse({ text }: { text: string }) {
  const [status, setStatus] = useState<'idle' | 'copying' | 'copied' | 'failed'>('idle');
  useEffect(() => {
    if (status !== 'copied') return;
    const timer = setTimeout(() => setStatus('idle'), 1500);
    return () => clearTimeout(timer);
  }, [status]);

  const copy = async () => {
    setStatus('copying');
    try {
      await navigator.clipboard.writeText(text);
      setStatus('copied');
    } catch {
      setStatus('failed');
    }
  };

  return (
    <div className="min-w-0">
      <Markdown>{text}</Markdown>
      <div className="mt-1 flex flex-wrap items-center gap-x-2">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-11 sm:h-8"
          title="Copy response as Markdown"
          aria-label={status === 'copied' ? 'Response copied' : 'Copy response as Markdown'}
          aria-live="polite"
          data-tour="copy-response"
          disabled={status === 'copying'}
          onClick={() => void copy()}
        >
          {status === 'copied' ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {status === 'copied' ? 'Copied' : status === 'copying' ? 'Copying…' : 'Copy Markdown'}
        </Button>
        {status === 'failed' && (
          <p className="text-danger text-xs" role="alert">Response not copied. Allow clipboard access and try again.</p>
        )}
      </div>
    </div>
  );
}
