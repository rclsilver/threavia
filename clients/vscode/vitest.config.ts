import { defineConfig } from 'vitest/config';

// The tests cover the logic that does not need an editor: everything that
// imports `vscode` is kept thin on purpose, so nothing here has to fake it.
export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    environment: 'node',
  },
});
