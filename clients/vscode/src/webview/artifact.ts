import { createDiagramRenderer, diagramFrame, type DiagramTheme } from '../../../shared/mermaid';

declare function acquireVsCodeApi(): { getState(): { fullWidth?: boolean } | undefined; setState(state: { fullWidth: boolean }): void };

const render = createDiagramRenderer(() => import('mermaid'));
const diagrams = [...document.querySelectorAll<HTMLElement>('.code-block')]
  .filter((block) => block.dataset.language?.toLowerCase() === 'mermaid')
  .map((block) => ({ block, code: block.querySelector('code')?.textContent ?? '' }));
let generation = 0;

function theme(): DiagramTheme {
  return document.body.classList.contains('vscode-dark') || document.body.classList.contains('vscode-high-contrast') ? 'dark' : 'default';
}

function updateDiagrams() {
  const current = ++generation;
  const scheme = theme();
  for (const { block, code } of diagrams) {
    const status = document.createElement('p');
    status.setAttribute('role', 'status');
    status.textContent = 'Rendering diagram…';
    block.replaceChildren(status);
    void render(code, scheme).then((svg) => {
      if (current !== generation) return;
      const frame = diagramFrame(svg, scheme);
      const iframe = document.createElement('iframe');
      iframe.title = 'Mermaid diagram';
      iframe.setAttribute('sandbox', '');
      iframe.referrerPolicy = 'no-referrer';
      iframe.srcdoc = frame.html;
      iframe.style.height = `${frame.height}px`;
      block.replaceChildren(iframe);
    }, () => {
      if (current !== generation) return;
      status.textContent = 'This diagram could not be rendered. Its source is shown below.';
      const pre = document.createElement('pre');
      pre.textContent = code;
      block.replaceChildren(status, pre);
    });
  }
}

for (const button of document.querySelectorAll<HTMLButtonElement>('[data-view]')) {
  button.addEventListener('click', () => {
    for (const panel of document.querySelectorAll<HTMLElement>('#preview, #source')) panel.hidden = panel.id !== button.dataset.view;
    for (const sibling of document.querySelectorAll<HTMLButtonElement>('[data-view]')) sibling.setAttribute('aria-pressed', String(sibling === button));
  });
}

// The panel reloads its page when it comes back into view; the width chosen
// is kept with it, as the conversation keeps its own. It is set on the root:
// the body's classes are the theme, and changing them redraws every diagram.
const vscode = acquireVsCodeApi();
const widthToggle = document.querySelector<HTMLButtonElement>('#full-width')!;
function setFullWidth(fullWidth: boolean) {
  document.documentElement.classList.toggle('full-width', fullWidth);
  widthToggle.setAttribute('aria-pressed', String(fullWidth));
}
setFullWidth(vscode.getState()?.fullWidth === true);
widthToggle.addEventListener('click', () => {
  const fullWidth = widthToggle.getAttribute('aria-pressed') !== 'true';
  setFullWidth(fullWidth);
  vscode.setState({ fullWidth });
});

const observer = new MutationObserver(updateDiagrams);
observer.observe(document.body, { attributes: true, attributeFilter: ['class', 'data-vscode-theme-kind'] });
window.addEventListener('pagehide', () => { generation++; observer.disconnect(); }, { once: true });
updateDiagrams();
