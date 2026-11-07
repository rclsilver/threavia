// jsdom ships without types and is here only for tests: this is the part of
// it they use, to run a webview page's own script in a window of its own.
declare module 'jsdom' {
  export class JSDOM {
    constructor(html: string, options?: { runScripts?: 'dangerously'; beforeParse?: (window: Window) => void });
    readonly window: Window & typeof globalThis;
  }
}
