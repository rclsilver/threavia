import { useEffect, useState } from 'react';

type Highlight = (code: string, language: string) => string;

/**
 * The languages an agent working on this kind of project actually emits.
 *
 * Imported one by one rather than through Shiki's full bundle: that bundle
 * registers every grammar it knows, which costs a lazy chunk per language —
 * Wolfram and Emacs Lisp included — for a client that will never show one.
 */
const GRAMMARS = {
  bash: () => import('@shikijs/langs/bash'),
  diff: () => import('@shikijs/langs/diff'),
  docker: () => import('@shikijs/langs/docker'),
  go: () => import('@shikijs/langs/go'),
  hcl: () => import('@shikijs/langs/hcl'),
  ini: () => import('@shikijs/langs/ini'),
  javascript: () => import('@shikijs/langs/javascript'),
  json: () => import('@shikijs/langs/json'),
  make: () => import('@shikijs/langs/make'),
  markdown: () => import('@shikijs/langs/markdown'),
  nix: () => import('@shikijs/langs/nix'),
  python: () => import('@shikijs/langs/python'),
  rust: () => import('@shikijs/langs/rust'),
  sql: () => import('@shikijs/langs/sql'),
  tsx: () => import('@shikijs/langs/tsx'),
  typescript: () => import('@shikijs/langs/typescript'),
  yaml: () => import('@shikijs/langs/yaml'),
};

/** What a writer may call a language, mapped to the grammar that handles it. */
const ALIASES: Record<string, keyof typeof GRAMMARS> = {
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  console: 'bash',
  dockerfile: 'docker',
  js: 'javascript',
  jsx: 'tsx',
  ts: 'typescript',
  golang: 'go',
  md: 'markdown',
  py: 'python',
  rs: 'rust',
  terraform: 'hcl',
  tf: 'hcl',
  toml: 'ini',
  yml: 'yaml',
  patch: 'diff',
};

function grammarFor(language: string): keyof typeof GRAMMARS | null {
  const name = language.toLowerCase();
  if (name in GRAMMARS) return name as keyof typeof GRAMMARS;
  return ALIASES[name] ?? null;
}

let loading: Promise<Highlight> | null = null;

/**
 * Loads the highlighter once, on the first code block, and never again.
 *
 * The JavaScript regex engine rather than the WebAssembly one: it is a fraction
 * of the size, and the difference only shows on grammars far more baroque than
 * these.
 */
function highlighter(): Promise<Highlight> {
  loading ??= (async () => {
    const [{ createHighlighterCore }, { createJavaScriptRegexEngine }, light, dark] =
      await Promise.all([
        import('shiki/core'),
        import('shiki/engine/javascript'),
        import('@shikijs/themes/github-light'),
        import('@shikijs/themes/github-dark'),
      ]);

    const shiki = await createHighlighterCore({
      themes: [light.default, dark.default],
      langs: [],
      engine: createJavaScriptRegexEngine(),
    });

    const loaded = new Set<string>();

    return (code, language) => {
      const grammar = grammarFor(language);
      if (grammar && !loaded.has(grammar)) {
        // Not awaited: the block renders plain now and colours itself on the
        // next message, which is sooner than it sounds and never blocks.
        loaded.add(grammar);
        void GRAMMARS[grammar]().then((module) => shiki.loadLanguage(module.default));
      }

      return shiki.codeToHtml(code, {
        lang: grammar && shiki.getLoadedLanguages().includes(grammar) ? grammar : 'text',
        themes: { light: 'github-light', dark: 'github-dark' },
        defaultColor: false,
      });
    };
  })();
  return loading;
}

/**
 * A fenced code block from agent output.
 *
 * It renders unhighlighted first and colours itself once the grammar is in: the
 * text is what matters, and waiting for a highlighter before showing it would
 * make the agent look slower than it is.
 */
export function CodeBlock({ language, code }: { language: string; code: string }) {
  const [html, setHtml] = useState<string | null>(null);

  useEffect(() => {
    let current = true;

    const paint = async () => {
      const highlight = await highlighter();
      if (current) setHtml(highlight(code, language));

      // The first block in a language renders before its grammar finished
      // loading, so one repaint picks up the colours.
      await new Promise((resolve) => setTimeout(resolve, 150));
      if (current) setHtml(highlight(code, language));
    };

    void paint().catch(() => {
      // Highlighting is decoration. Losing it must not lose the code.
    });

    return () => {
      current = false;
    };
  }, [code, language]);

  if (html === null) {
    return (
      <pre>
        <code>{code}</code>
      </pre>
    );
  }
  return <div className="shiki-block" dangerouslySetInnerHTML={{ __html: html }} />;
}
