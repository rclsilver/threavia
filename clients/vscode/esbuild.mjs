// Bundles the extension into one file, which is what VS Code loads, and the
// conversation page into another, which the webview loads.
//
// One file rather than the compiled tree: the package then carries no
// node_modules at all, so it installs the same everywhere and starts without
// resolving hundreds of modules.
import * as esbuild from 'esbuild';

const production = process.argv.includes('--production');
const watch = process.argv.includes('--watch');

const common = {
  bundle: true,
  sourcemap: !production,
  minify: production,
  logLevel: 'info',
};

const contexts = await Promise.all([
  esbuild.context({
    ...common,
    entryPoints: ['src/extension.ts'],
    format: 'cjs',
    platform: 'node',
    // The oldest Node a supported VS Code ships with (engines.vscode ^1.95).
    target: 'node20',
    outfile: 'dist/extension.js',
    // Provided by the editor at runtime, never bundled.
    external: ['vscode'],
  }),
  // The page runs in the editor's Chromium, with no access to Node or to the
  // network: everything it needs, the markdown renderer and the icon font
  // included, is in these files.
  esbuild.context({
    ...common,
    entryPoints: { webview: 'src/webview/main.ts', 'webview-style': 'src/webview/style.css' },
    format: 'iife',
    platform: 'browser',
    target: 'chrome120',
    outdir: 'dist',
    loader: { '.ttf': 'file' },
    assetNames: '[name]',
  }),
]);

if (watch) {
  await Promise.all(contexts.map((context) => context.watch()));
} else {
  await Promise.all(contexts.map((context) => context.rebuild()));
  await Promise.all(contexts.map((context) => context.dispose()));
}
