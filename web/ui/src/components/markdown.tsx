import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

import { CodeBlock } from '@/components/code-block';

/**
 * Agent output, rendered as the markdown it is.
 *
 * Memoised on the text: a timeline re-renders on every event, and reparsing
 * every message each time is the one thing that makes a long session feel slow.
 */
/** Flattens the children of a code node into the text it holds. */
function textOf(children: React.ReactNode): string {
  if (typeof children === 'string') return children;
  if (typeof children === 'number') return String(children);
  if (Array.isArray(children)) return children.map(textOf).join('');
  return '';
}

export const Markdown = memo(function Markdown({ children }: { children: string }) {
  return (
    <div className="prose-agent">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          code({ className, children, ...props }) {
            const language = /language-(\w+)/.exec(className ?? '')?.[1];
            const text = textOf(children).replace(/\n$/, '');

            // An inline span has no language and no newline; a fenced block is
            // what gets highlighted.
            if (!language && !text.includes('\n')) {
              return (
                <code className={className} {...props}>
                  {children}
                </code>
              );
            }
            return <CodeBlock language={language ?? 'text'} code={text} />;
          },
          // The block wrapper comes from CodeBlock, so this one would nest.
          pre: ({ children }) => <>{children}</>,
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
});
