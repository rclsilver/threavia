import { useEffect, useState } from 'react';

import { createDiagramRenderer, diagramFrame, type DiagramTheme } from '../../../../clients/shared/mermaid';
import { CodeBlock } from '@/components/code-block';

const render = createDiagramRenderer(() => import('mermaid'));
const scheme = (): DiagramTheme => document.documentElement.dataset.scheme === 'dark' ? 'dark' : 'default';

export function MermaidBlock({ code }: { code: string }) {
  const [theme, setTheme] = useState(scheme);
  const [result, setResult] = useState<{ code: string; theme: DiagramTheme; svg?: string; failed?: boolean }>();
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(scheme()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-scheme'] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    let current = true;
    void render(code, theme).then(
      (svg) => { if (current) setResult({ code, theme, svg }); },
      () => { if (current) setResult({ code, theme, failed: true }); },
    );
    return () => { current = false; };
  }, [code, theme]);

  if (result?.code !== code || result.theme !== theme) return <p role="status" className="text-muted text-sm">Rendering diagram…</p>;
  if (result.failed || !result.svg) return <div><p role="status" className="text-muted mb-2 text-sm">This diagram could not be rendered. Its source is shown below.</p><CodeBlock code={code} language="mermaid" /></div>;
  const frame = diagramFrame(result.svg, theme);
  return <iframe title="Mermaid diagram" sandbox="" referrerPolicy="no-referrer" srcDoc={frame.html} style={{ height: frame.height }} className="border-border w-full rounded-md border" />;
}
