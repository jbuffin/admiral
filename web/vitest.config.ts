import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    passWithNoTests: true,
    // A test that depends on order fails at once and prints its seed.
    sequence: { shuffle: true },
    projects: [
      { extends: true, test: { name: 'logic', environment: 'node', include: ['src/**/*.test.ts'] } },
      {
        extends: true,
        esbuild: { jsx: 'automatic', jsxImportSource: 'preact' },
        test: { name: 'component', environment: 'happy-dom', include: ['src/**/*.test.tsx'], setupFiles: ['./src/testSetup.ts'] },
      },
    ],
  },
});
