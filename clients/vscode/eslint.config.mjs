import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  // Generated from the API contract, and never edited by hand.
  { ignores: ['dist', 'src/api/schema.d.ts', '*.vsix'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommendedTypeChecked],
    files: ['**/*.ts'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.node,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    // The build scripts are plain JavaScript, outside the type-checked project.
    extends: [js.configs.recommended],
    files: ['**/*.mjs'],
    languageOptions: { ecmaVersion: 2022, globals: globals.node },
  },
);
