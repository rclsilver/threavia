import { ARTIFACT_TEXT_LIMIT } from '../../../shared/markdown-artifact';
import { renderMarkdown } from '../webview/markdown';

const escape = (text: string) => text.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);

export function markdownArtifactPage(options: { title: string; text: string; cspSource: string; scriptUri: string; styleUri: string; nonce: string }) {
  const text = options.text.slice(0, ARTIFACT_TEXT_LIMIT);
  const policy = `default-src 'none'; script-src 'nonce-${options.nonce}'; style-src ${options.cspSource} 'unsafe-inline'; font-src ${options.cspSource}; img-src data:; frame-src 'self'; base-uri 'none'; form-action 'none'`;
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="${escape(policy)}"><link rel="stylesheet" href="${escape(options.styleUri)}"><title>${escape(options.title)}</title></head><body>
<header class="artifact-toolbar"><strong>${escape(options.title)}</strong><div role="group" aria-label="Markdown view"><button data-view="preview" aria-pressed="true">Preview</button><button data-view="source" aria-pressed="false">Source</button></div></header>
${options.text.length > ARTIFACT_TEXT_LIMIT ? '<p class="artifact-limit" role="status">This preview is limited to 200,000 characters. Save the file to read the full document.</p>' : ''}
<main class="artifact-document"><section id="preview" class="prose" aria-label="Rendered Markdown">${renderMarkdown(text)}</section><pre id="source" hidden aria-label="Markdown source"><code>${escape(text)}</code></pre></main>
<script nonce="${escape(options.nonce)}" src="${escape(options.scriptUri)}"></script></body></html>`;
}
