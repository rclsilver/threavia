import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

// Protocol and lifecycle tests run without an editor. CoreAuth explicitly
// mocks only the host events and SecretStorage that surround its real logic.
export default defineConfig({
  resolve: { alias: { vscode: fileURLToPath(new URL('./test/vscode-mock.ts', import.meta.url)), '@': fileURLToPath(new URL('../../web/ui/src', import.meta.url)) } },
  test: {
    include: ['test/**/*.test.ts'],
    environment: 'node',
  },
});
