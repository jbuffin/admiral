import { defineConfig } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const bin = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'bin', 'admiral');
if (!existsSync(bin)) {
  throw new Error('bin/admiral not found: run make build');
}

// Workers load this file again, so pick the port and home once and share them
// through the environment.
if (!process.env.ADMIRAL_E2E_PORT) {
  process.env.ADMIRAL_E2E_PORT = execFileSync(process.execPath, [
    '-e',
    "const s=require('net').createServer().listen(0,'127.0.0.1',()=>{console.log(s.address().port);s.close()})",
  ]).toString().trim();
  process.env.ADMIRAL_E2E_HOME = join(mkdtempSync(join(tmpdir(), 'admiral-e2e-')), 'home');
}
const port = process.env.ADMIRAL_E2E_PORT;
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './e2e',
  retries: 0,
  workers: process.env.CI ? undefined : 2,
  reporter: [['list']],
  use: {
    baseURL,
    trace: process.env.CI ? 'retain-on-failure' : { mode: 'retain-on-failure', screenshots: false },
  },
  webServer: {
    command: `node scripts/serve-e2e.mjs ${bin} 127.0.0.1:${port}`,
    gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
    url: `${baseURL}/`,
    timeout: 15_000,
    reuseExistingServer: false,
    env: { ADMIRAL_HOME: process.env.ADMIRAL_E2E_HOME! },
  },
  projects: [
    {
      name: 'ipad-landscape',
      use: { browserName: 'webkit', viewport: { width: 1194, height: 834 }, hasTouch: true, isMobile: true },
    },
    {
      name: 'ipad-portrait',
      use: { browserName: 'webkit', viewport: { width: 834, height: 1194 }, hasTouch: true, isMobile: true },
    },
    {
      name: 'desktop',
      use: { browserName: 'webkit', viewport: { width: 1440, height: 900 } },
    },
  ],
});
