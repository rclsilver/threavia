import { describe, expect, it } from 'vitest';
import { isMarkdown, ARTIFACT_TEXT_LIMIT } from '../../shared/markdown-artifact';
import { artifactKind } from '../src/conversation/format';
import { kindOf } from '../../../web/ui/src/lib/artifact';
import { markdownArtifactPage } from '../src/conversation/markdown-artifact';

function page(text: string) {
  return markdownArtifactPage({ title: 'Report <test>', text, cspSource: 'https://extension.test', scriptUri: 'https://extension.test/artifact.js', styleUri: 'https://extension.test/artifact.css', nonce: 'nonce-for-tests' });
}

describe('Markdown artifact identification in both clients', () => {
  it.each([
    ['text/markdown; charset=utf-8', 'report'], ['TEXT/X-MARKDOWN', 'report'],
    ['application/markdown', 'report'], ['text/plain', 'REPORT.MD'],
    ['application/octet-stream', 'report.markdown'], ['', 'report.mdown'], ['', 'report.mkd'],
  ])('opens %s / %s as Markdown', (mimeType, filename) => {
    expect(isMarkdown(mimeType, filename)).toBe(true);
    expect(artifactKind(mimeType, filename)).toBe('markdown');
    expect(kindOf(mimeType, filename)).toBe('markdown');
  });
  it('keeps text, HTML, media and unknown files on their existing viewers', () => {
    for (const [mimeType, filename, expected] of [
      ['text/plain', 'notes.txt', 'text'], ['text/html', 'report.html', 'html'],
      ['image/png', 'plot.png', 'image'], ['', 'audio.wav', 'audio'], ['', 'video.mp4', 'video'], ['', 'unknown.bin', 'other'],
    ]) {
      expect(artifactKind(mimeType, filename)).toBe(expected);
      expect(kindOf(mimeType, filename)).toBe(expected);
    }
  });
});

describe('Markdown artifact page', () => {
  it('starts with the rendered document and keeps the source separate and escaped', () => {
    const source = '# Heading\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```mermaid\nflowchart LR\nA --> B\n```\n\n<script>alert(1)</script>\n[unsafe](javascript:alert(1))';
    const html = page(source);
    expect(html).toContain('<h1>Heading</h1>');
    expect(html).toContain('<table>');
    expect(html).toContain('data-language="mermaid"');
    expect(html).toContain('data-view="preview" aria-pressed="true"');
    expect(html).toContain('id="source" hidden');
    expect(html).toContain('&#60;script&#62;alert(1)&#60;/script&#62;');
    expect(html).not.toContain('<script>alert(1)</script>');
    expect(html).not.toContain('href="javascript:');
    expect(html).toContain("default-src &#39;none&#39;");
    expect(html).toContain("script-src &#39;nonce-nonce-for-tests&#39;");
  });
  it('warns when either preview or source is truncated', () => {
    const html = page('x'.repeat(ARTIFACT_TEXT_LIMIT) + 'END-OF-FILE');
    expect(html).toContain('Save the file to read the full document.');
    expect(html).not.toContain('END-OF-FILE');
  });
});
