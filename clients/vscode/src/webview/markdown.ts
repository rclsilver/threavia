import MarkdownIt from 'markdown-it';

/**
 * The agent's prose, rendered as the markdown it is, safely.
 *
 * Raw HTML is off, so a tag the agent wrote is shown as text and never parsed;
 * markdown-it escapes everything else it emits and refuses `javascript:` and
 * similar links. Images are not loaded: the page may not reach the network,
 * so an image is said as a link instead of drawn as a broken one.
 */
const md = new MarkdownIt({ html: false, linkify: true, breaks: false, typographer: false });
// Only what is written as an address becomes a link: `notes.md` is a file,
// not a site in Moldova.
md.linkify.set({ fuzzyLink: false, fuzzyEmail: false });

md.renderer.rules.image = (tokens, index) => {
  const token = tokens[index];
  const alt = md.utils.escapeHtml(token.content || 'image');
  const src = md.utils.escapeHtml(token.attrGet('src') ?? '');
  return `<a href="${src}">${alt}</a>`;
};

// A fenced block becomes a frame with its language and a copy button, the
// button drawn by the page once the HTML is in place (see enhanceCode).
md.renderer.rules.fence = (tokens, index) => {
  const token = tokens[index];
  const language = token.info.trim().split(/\s+/)[0] ?? '';
  const code = md.utils.escapeHtml(token.content.replace(/\n$/, ''));
  const label = language ? `<span class="code-language">${md.utils.escapeHtml(language)}</span>` : '';
  return `<div class="code-block" data-language="${md.utils.escapeHtml(language)}">${label}<pre><code>${code}</code></pre></div>`;
};

const cache = new Map<string, string>();

/**
 * The HTML of a message. Kept by text, because the timeline redraws on every
 * event and reparsing every message each time is what makes a long Session
 * feel slow.
 */
export function renderMarkdown(text: string): string {
  let html = cache.get(text);
  if (html === undefined) {
    html = md.render(text);
    if (cache.size > 2000) cache.clear();
    cache.set(text, html);
  }
  return html;
}
