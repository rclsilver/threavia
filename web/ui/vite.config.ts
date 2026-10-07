import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath, URL } from 'node:url';

// In development the client is served by Vite, not by Core, so editing a
// component reloads the page without rebuilding or restarting the Go binary.
// Everything the client calls is proxied to Core, which keeps the browser on a
// single origin: no CORS, and the SSE stream behaves exactly as it does in
// production.
//
// THREAVIA_CORE_URL points the proxy elsewhere, which is what the dev container
// uses to reach a Core running on the host.
const core = process.env.THREAVIA_CORE_URL ?? 'http://localhost:8080';

const proxied = {
  target: core,
  changeOrigin: true,
  // The event stream must not be buffered: a proxy that waits for a complete
  // response would hold every event until the Job ends.
  ws: false,
};

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': proxied,
      '/healthz': proxied,
      '/readyz': proxied,
      '/version': proxied,
    },
  },
  build: {
    // Embedded in the Go binary, so the output is self-contained.
    outDir: 'dist',
    emptyOutDir: true,
    // No source map: it would add three megabytes to every Core binary for a
    // debugging aid that belongs in the dev server, where Vite provides it.
    sourcemap: false,
    rollupOptions: {
      output: {
        // The vendor code changes on a dependency bump, the application on every
        // commit. Splitting them lets a browser keep the larger half across
        // deploys. React itself is left to Rollup: naming it only produced an
        // empty chunk, because the entry pulls it in anyway.
        manualChunks: {
          tanstack: ['@tanstack/react-query', '@tanstack/react-router', '@tanstack/react-virtual'],
          markdown: ['react-markdown', 'remark-gfm'],
        },
      },
    },
  },
});
