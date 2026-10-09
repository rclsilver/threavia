/**
 * Building the page without a framework and without HTML strings.
 *
 * Every element is made with the DOM, every text set as text: what the agent
 * wrote can never become markup, except through the markdown renderer, whose
 * raw HTML is off.
 */

type Child = Node | string | false | null | undefined;
type Attributes = Record<string, string | number | boolean | undefined | ((event: Event) => void)>;

export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attributes: Attributes = {},
  ...children: (Child | Child[])[]
): HTMLElementTagNameMap[K] {
  const element = document.createElement(tag);
  for (const [name, value] of Object.entries(attributes)) {
    if (value === undefined || value === false) continue;
    if (typeof value === 'function') {
      element.addEventListener(name.replace(/^on/, '').toLowerCase(), value);
    } else if (name === 'class') {
      element.className = String(value);
    } else {
      element.setAttribute(name, value === true ? '' : String(value));
    }
  }
  append(element, children.flat());
  return element;
}

export function append(parent: Node, children: Child[]) {
  for (const child of children) {
    if (child === false || child === null || child === undefined) continue;
    parent.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
}

/** A codicon, decorative: the text beside it says the same. */
export function icon(name: string, extra = ''): HTMLSpanElement {
  return h('span', { class: `codicon codicon-${name}${extra ? ` ${extra}` : ''}`, 'aria-hidden': 'true' });
}

/** A real button, so the keyboard and a screen reader get one for free. */
export function button(
  label: Child | Child[],
  onClick: (event: Event) => void,
  attributes: Attributes = {},
): HTMLButtonElement {
  return h('button', { type: 'button', ...attributes, onclick: onClick }, ...(Array.isArray(label) ? label : [label]));
}
