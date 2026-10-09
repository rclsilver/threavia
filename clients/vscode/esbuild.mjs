// Bundles the extension into one file, which is what VS Code loads.
//
// One file rather than the compiled tree: the package then carries no
// node_modules at all, so it installs the same everywhere and starts without
// resolving hundreds of modules.
import * as esbuild from 'esbuild';

const production = process.argv.includes('--production');
const watch = process.argv.includes('--watch');

const context = await esbuild.context({
  entryPoints: ['src/extension.ts'],
  bundle: true,
  format: 'cjs',
  platform: 'node',
  // The oldest Node a supported VS Code ships with (engines.vscode ^1.95).
  target: 'node20',
  outfile: 'dist/extension.js',
  // Provided by the editor at runtime, never bundled.
  external: ['vscode'],
  sourcemap: !production,
  minify: production,
  logLevel: 'info',
});

if (watch) {
  await context.watch();
} else {
  await context.rebuild();
  await context.dispose();
}
