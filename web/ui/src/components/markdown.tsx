import { memo } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';

import { MermaidBlock } from '@/components/mermaid-block';
import { CodeBlock } from '@/components/code-block';
import { cn } from '@/lib/utils';

/** Flattens the children of a code node into the text it holds. */
function textOf(children: React.ReactNode): string {
  if (typeof children === 'string') return children;
  if (typeof children === 'number') return String(children);
  if (Array.isArray(children)) return children.map(textOf).join('');
  return '';
}

type MdNode = { type: string; value?: string; children?: MdNode[] };

/**
 * Every newline in prose becomes a line break, as remark-breaks does. A
 * question is written like a chat message, one choice per line, and markdown
 * would otherwise join those lines into one. Code keeps its own newlines: its
 * text is a value, not a text child.
 */
function remarkLineBreaks() {
  const split = (node: MdNode) => {
    if (!node.children) return;
    node.children = node.children.flatMap((child): MdNode[] => {
      if (child.type !== 'text' || !child.value?.includes('\n')) {
        split(child);
        return [child];
      }
      return child.value.split(/\r?\n/).flatMap((line, index): MdNode[] => [
        ...(index ? [{ type: 'break' }] : []),
        { type: 'text', value: line },
      ]);
    });
  };
  return split;
}

const plugins = [remarkGfm];
const pluginsWithBreaks = [remarkGfm, remarkLineBreaks];

/**
 * Agent output, rendered as the markdown it is.
 *
 * Memoised on the text: a timeline re-renders on every event, and reparsing
 * every message each time is the one thing that makes a long session feel slow.
 *
 * `breaks` keeps single newlines. `images={false}` says an image as its alt
 * text instead of fetching it: text shown outside its Session, such as a
 * question in the attention list, must not load an address the agent chose.
 */
export const Markdown = memo(function Markdown({
  children,
  diagrams = false,
  breaks = false,
  images = true,
  className,
}: {
  children: string;
  diagrams?: boolean;
  breaks?: boolean;
  images?: boolean;
  className?: string;
}) {
  const components: Components = {
    code({ className, children, ...props }) {
      const language = /language-(\w+)/.exec(className ?? '')?.[1];
      const text = textOf(children).replace(/\n$/, '');

      if (diagrams && language?.toLowerCase() === 'mermaid') return <MermaidBlock code={text} />;

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
    ...(!images && { img: ({ alt }) => <>{alt ? `[${alt}]` : '[image]'}</> }),
  };

  return (
    <div className={cn('prose-agent', className)}>
      <ReactMarkdown remarkPlugins={breaks ? pluginsWithBreaks : plugins} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  );
});
