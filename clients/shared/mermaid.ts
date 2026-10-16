export type DiagramTheme = 'default' | 'dark';

interface Engine {
  initialize(config: {
    startOnLoad: boolean; securityLevel: 'strict'; theme: DiagramTheme;
    maxTextSize: number; maxEdges: number; suppressErrorRendering: boolean; secure: string[];
  }): void;
  render(id: string, text: string, container: HTMLElement): Promise<{ svg: string }>;
}

let nextId = 0;

/** Mermaid has global configuration: serialize initialization and rendering. */
export function createDiagramRenderer(load: () => Promise<{ default: Engine }>) {
  let queue: Promise<unknown> = Promise.resolve();
  return (text: string, theme: DiagramTheme): Promise<string> => {
    const result = queue.then(async () => {
      if (text.length > 50_000) throw new Error('Diagram is too large to preview.');
      const { default: mermaid } = await load();
      const host = document.createElement('div');
      host.style.cssText = 'position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none';
      document.body.append(host);
      try {
        mermaid.initialize({
          startOnLoad: false, securityLevel: 'strict', theme,
          maxTextSize: 50_000, maxEdges: 500, suppressErrorRendering: true,
          secure: ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'maxEdges', 'suppressErrorRendering'],
        });
        return (await mermaid.render(`threavia-diagram-${++nextId}`, text, host)).svg;
      } finally {
        host.remove();
      }
    });
    // A malformed diagram must not prevent subsequent diagrams from rendering.
    queue = result.catch(() => undefined);
    return result;
  };
}

/** Static, scriptless diagrams keep their natural size and scroll on phones. */
export function diagramFrame(svg: string, theme: DiagramTheme) {
  const viewBox = /viewBox="([\d.e+\- ]+)"/i.exec(svg)?.[1].trim().split(/\s+/).map(Number);
  const width = viewBox?.[2];
  const height = viewBox?.[3];
  const valid = (value: number | undefined): value is number => value !== undefined && Number.isFinite(value) && value > 0;
  const diagramWidth = valid(width) ? Math.min(width, 10_000) : 800;
  const frameHeight = valid(height) ? Math.min(900, Math.max(120, height + 24)) : 400;
  return {
    height: frameHeight,
    html: `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"><style>html{color-scheme:${theme === 'dark' ? 'dark' : 'light'}}body{margin:0;padding:8px;box-sizing:border-box;background:${theme === 'dark' ? '#171717' : '#ffffff'};width:fit-content;min-width:100%}svg{display:block;width:${diagramWidth}px!important;max-width:none!important;height:auto!important;margin:auto}</style></head><body>${svg}</body></html>`,
  };
}
