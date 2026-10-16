// @vitest-environment jsdom
import { beforeAll, describe, expect, it, vi } from 'vitest';
import mermaid from 'mermaid';
import { createDiagramRenderer, diagramFrame } from '../../shared/mermaid';

// jsdom has no SVG layout engine. Supply geometry while using the real parser,
// SVG generator and sanitizer; this does not assert browser layout or appearance.
beforeAll(() => {
  Object.defineProperty(SVGElement.prototype, 'getBBox', { configurable: true, value: () => ({ x: 0, y: 0, width: 160, height: 24 }) });
  Object.defineProperty(SVGElement.prototype, 'getComputedTextLength', { configurable: true, value: () => 160 });
});

const render = createDiagramRenderer(() => Promise.resolve({ default: mermaid }));

describe('Mermaid artifact rendering', () => {
  it('renders real flow and sequence diagrams with distinct IDs and cleans measurement nodes', async () => {
    const [flow, sequence] = await Promise.all([
      render('flowchart LR\nA[Hello] --> B[World]', 'default'),
      render('sequenceDiagram\nAlice->>Bob: Hello', 'dark'),
    ]);
    expect(flow).toContain('<svg');
    expect(flow).toContain('Hello');
    expect(sequence).toContain('<svg');
    expect(sequence).toContain('Alice');
    const firstId = /id="(threavia-diagram-\d+)"/.exec(flow)?.[1];
    const secondId = /id="(threavia-diagram-\d+)"/.exec(sequence)?.[1];
    expect(firstId).toBeTruthy();
    expect(secondId).toBeTruthy();
    expect(firstId).not.toBe(secondId);
    expect(document.body.children).toHaveLength(0);
  });
  it('recovers after invalid syntax and refuses an oversized diagram', async () => {
    await expect(render('not-a-valid-diagram', 'default')).rejects.toThrow();
    expect(document.body.children).toHaveLength(0);
    await expect(render('x'.repeat(50_001), 'default')).rejects.toThrow('too large');
    expect(await render('flowchart TD\nA --> B', 'default')).toContain('<svg');
  });
  it('prevents diagram directives from enabling click handlers or HTML scripts', async () => {
    const svg = await render('%%{init: {"securityLevel":"loose"}}%%\nflowchart LR\nA[Safe] --> B[Text]\nclick A "javascript:alert(1)"', 'default');
    expect(mermaid.mermaidAPI.getConfig().securityLevel).toBe('strict');
    expect(svg).not.toContain('javascript:');
    expect(svg).not.toContain('<script');
    expect(svg).not.toContain('onclick=');
  });
  it('serializes theme configuration and recovers the queue after an engine failure', async () => {
    let done!: (svg: { svg: string }) => void;
    const initialize = vi.fn<(config: { theme: string }) => void>();
    const engine = { initialize, render: vi.fn().mockImplementationOnce(() => new Promise((resolve) => { done = resolve; })).mockRejectedValueOnce(new Error('bad graph')).mockResolvedValueOnce({ svg: 'last' }) };
    const run = createDiagramRenderer(() => Promise.resolve({ default: engine }));
    const first = run('first', 'dark');
    const failed = run('failed', 'default');
    const last = run('last', 'default');
    const failure = expect(failed).rejects.toThrow('bad graph');
    await vi.waitFor(() => expect(engine.render).toHaveBeenCalledOnce());
    expect(initialize).toHaveBeenCalledOnce();
    done({ svg: 'first' });
    expect(await first).toBe('first');
    await failure;
    expect(await last).toBe('last');
    expect(initialize.mock.calls.map(([config]) => config.theme)).toEqual(['dark', 'default', 'default']);
    expect(document.body.children).toHaveLength(0);
  });
  it('bounds frame dimensions and applies a scriptless network policy', () => {
    const frame = diagramFrame('<svg viewBox="0 0 400 99999"></svg>', 'dark');
    expect(frame.height).toBe(900);
    expect(frame.html).toContain("default-src 'none'");
    expect(frame.html).toContain("base-uri 'none'");
    expect(frame.html).toContain('color-scheme:dark');
    expect(frame.html).toContain('width:400px');
    expect(diagramFrame('<svg viewBox="0 0 NaN -1"></svg>', 'default').height).toBe(400);
  });
});
