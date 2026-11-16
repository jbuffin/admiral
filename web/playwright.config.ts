import { defineConfig } from '@playwright/test';
import { existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const bin = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'bin', 'admiral');
if (!existsSync(bin)) {
  throw new Error('bin/admiral not found: run make build');
}

const phoneSpecs = /-phone\.spec\.ts$/;

export default defineConfig({
  testDir: './e2e',
  retries: 0,
  workers: process.env.CI ? undefined : 2,
  reporter: [['list']],
  use: {
    // The journeys fake the API with page.route, which a worker would bypass; the install journeys allow it.
    serviceWorkers: 'block',
    trace: process.env.CI ? 'retain-on-failure' : { mode: 'retain-on-failure', screenshots: false },
  },
  projects: [
    {
      name: 'ipad-landscape',
      testIgnore: phoneSpecs,
      use: { browserName: 'webkit', viewport: { width: 1194, height: 834 }, hasTouch: true, isMobile: true },
    },
    {
      name: 'phone',
      testMatch: phoneSpecs,
      use: { browserName: 'webkit', viewport: { width: 402, height: 874 }, hasTouch: true, isMobile: true },
    },
  ],
});
