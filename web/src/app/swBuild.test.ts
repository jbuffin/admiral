import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { build } from 'vite';
import { describe, expect, it } from 'vitest';

describe('the built service worker', () => {
  it('handles pushes and taps, and is one file: a classic worker cannot load a chunk, so it has no import', async () => {
    const out = mkdtempSync(join(tmpdir(), 'admiral-sw-'));
    try {
      // The real multi-page config: a module the app and the worker share would split into a chunk the worker imports.
      await build({ configFile: resolve(__dirname, '../../vite.config.ts'), logLevel: 'silent', build: { outDir: out, emptyOutDir: true } });
      const sw = readFileSync(join(out, 'sw.js'), 'utf8');
      expect(sw).toContain('notificationclick');
      expect(sw).toMatch(/["']push["']/);
      expect(sw).not.toMatch(/(^|[;}\s])import\s*[{"'*\w(]/);
    } finally {
      rmSync(out, { recursive: true, force: true });
    }
  }, 180_000);
});
