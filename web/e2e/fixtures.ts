import { test as base, expect } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const bin = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'bin', 'admiral');

// One Admiral per test: its own ADMIRAL_HOME, its own config.json and its own
// port, so scenarios can each start from a different config.
export interface Admiral {
  /** The temp ADMIRAL_HOME. Everything a test writes goes under it. */
  home: string;
  url: string;
  /** Writes config.json atomically (temp file + rename), as an editor would. */
  writeConfig(config: unknown): void;
  /** Writes config.json atomically with this exact text, valid or not. */
  writeConfigText(text: string): void;
  /** An executable fake harness under home; returns its absolute path. */
  fakeHarness(name: string): string;
  /** A path under home that is not there, for a dock whose executable is missing. */
  missingHarness(name: string): string;
  /** Starts the server on a free port, with whatever config.json holds. */
  start(): Promise<void>;
}

class Instance implements Admiral {
  home = mkdtempSync(join(tmpdir(), 'admiral-ui-'));
  url = '';
  private proc?: ChildProcess;

  writeConfigText(text: string) {
    mkdirSync(this.home, { recursive: true });
    const tmp = join(this.home, 'config.json.tmp');
    writeFileSync(tmp, text, { mode: 0o600 });
    renameSync(tmp, join(this.home, 'config.json'));
  }

  writeConfig(config: unknown) {
    this.writeConfigText(JSON.stringify(config, null, 2));
  }

  fakeHarness(name: string) {
    const dir = join(this.home, 'fake-bin');
    mkdirSync(dir, { recursive: true });
    const path = join(dir, name);
    writeFileSync(path, '#!/bin/sh\nexit 0\n');
    chmodSync(path, 0o755);
    return path;
  }

  missingHarness(name: string) {
    return join(this.home, 'no-such-dir', name);
  }

  start() {
    return new Promise<void>((done, fail) => {
      // A harness identity variable from the engineer's shell must not reach the server.
      const env: NodeJS.ProcessEnv = { ...process.env, ADMIRAL_HOME: this.home };
      for (const v of ['CLAUDE_CONFIG_DIR', 'CODEX_HOME', 'COPILOT_HOME']) delete env[v];
      const proc = spawn(bin, ['--listen', '127.0.0.1:0'], {
        env,
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      this.proc = proc;
      let out = '';
      const timer = setTimeout(() => fail(new Error(`admiral did not print its URL: ${out}`)), 15_000);
      proc.once('error', fail);
      proc.once('exit', (code) => fail(new Error(`admiral exited with ${code}: ${out}`)));
      proc.stdout!.on('data', (b: Buffer) => {
        out += b.toString();
        const m = /Admiral is running at (http:\/\/127\.0\.0\.1:\d+\/)/.exec(out);
        if (m) {
          clearTimeout(timer);
          this.url = m[1];
          done();
        }
      });
      proc.stderr!.on('data', (b: Buffer) => (out += b.toString()));
    });
  }

  async stop() {
    const proc = this.proc;
    if (proc && proc.exitCode === null && proc.signalCode === null) {
      proc.removeAllListeners('exit');
      const gone = new Promise((r) => proc.once('exit', r));
      proc.kill('SIGTERM');
      const hard = setTimeout(() => proc.kill('SIGKILL'), 3000);
      await gone;
      clearTimeout(hard);
    }
    rmSync(this.home, { recursive: true, force: true });
  }
}

export const test = base.extend<{ admiral: Admiral }>({
  admiral: async ({}, use) => {
    const a = new Instance();
    try {
      await use(a);
    } finally {
      await a.stop();
    }
  },
});

export { expect };
