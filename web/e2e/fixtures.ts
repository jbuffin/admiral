import { test as base, expect } from '@playwright/test';
import { type ChildProcess, spawn, spawnSync } from 'node:child_process';
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { randomBytes } from 'node:crypto';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const bin = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'bin', 'admiral');

const fakeGhDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'testdata', 'fakegh');

const testPlugin = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'testplugin', 'plugin.py');

// The test writes what the plugin should list or do here, and reads what it logged.
export interface Flags {
  /** The directory to put in the source's settings as `flagdir`. */
  dir: string;
  /** Orders per panel id, read on every orders.list. */
  orders(byPanel: Record<string, unknown[]>): void;
  /** Panels, read on every panels.list. */
  panels(list: { id: string; title: string }[]): void;
  /** A flag file (crash-on-initialize, slow-initialize…) or a do-<command> file. */
  put(name: string, content?: string): void;
  remove(name: string): void;
  starts(): number;
}

// One Admiral per test, so scenarios can each start from a different config.
export interface Admiral {
  home: string;
  url: string;
  /** Writes config.json atomically (temp file + rename), as an editor would. */
  writeConfig(config: unknown): void;
  /** Writes config.json atomically with this exact text, valid or not. */
  writeConfigText(text: string): void;
  fakeHarness(name: string): string;
  /** A path under home that is not there, for a dock whose executable is missing. */
  missingHarness(name: string): string;
  installTestPlugin(folder?: string): void;
  /** The test-only orders source as a config `sources` entry; `extra` sits beside its settings. */
  ordersSource(key?: string, name?: string, extra?: Record<string, unknown>): Record<string, unknown>;
  flags(label: string): Flags;
  /** The fake `gh` (e2e/testdata/fakegh) for the built-in GitHub source: pass `env` to `start`; `token` signs an account in ('' is the host's active one). */
  fakeGh(): { dir: string; env: Record<string, string>; token(user: string, value: string): void };
  /** `lan` listens on 0.0.0.0 and fills `lanUrl` and `token`; `env` wins over the server's environment. */
  start(opts?: { lan?: boolean; env?: Record<string, string> }): Promise<void>;
  /** With `start({ lan: true })`: the Mac's non-loopback address (http://<ip>:<port>/) and the token the server printed. */
  lanUrl: string;
  token: string;
  /** Stops the server process only: the home, the ships and their tmux server stay. */
  stopServer(): Promise<void>;
  resumeServer(): Promise<void>;
  restart(): Promise<void>;
  /** Writes a new token file, as `admiral token rotate` would; the server picks it up on its next start. Returns the token. */
  rotateToken(): string;
}

class Instance implements Admiral {
  home = mkdtempSync(join(tmpdir(), 'admiral-ui-'));
  url = '';
  private proc?: ChildProcess;
  log = '';
  /** Set when a server process exits without stopServer asking it to. */
  died = '';
  onDeath?: () => void;

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

  installTestPlugin(folder = 'testplug') {
    const dir = join(this.home, 'plugins', folder);
    mkdirSync(dir, { recursive: true });
    copyFileSync(testPlugin, join(dir, 'plugin.py'));
    chmodSync(join(dir, 'plugin.py'), 0o755);
    writeFileSync(join(dir, 'icon.svg'), '<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>');
    writeFileSync(
      join(dir, 'plugin.json'),
      JSON.stringify({ name: folder, description: `Test plugin ${folder}`, command: './plugin.py', args: [], icon: 'icon.svg' }),
    );
  }

  ordersSource(key = 'orders', name = 'Orders', extra: Record<string, unknown> = {}) {
    if (!existsSync(join(this.home, 'plugins', 'testplug', 'plugin.json'))) this.installTestPlugin();
    const f = this.flags(key);
    f.panels([{ id: 'orders', title: 'Orders' }]);
    return { plugin: 'testplug', fleet: 'work', name, settings: { flagdir: f.dir, storeOrders: true, capabilities: ['add', 'edit', 'remove'] }, ...extra };
  }

  flags(label: string): Flags {
    const dir = join(this.home, 'flags', label);
    mkdirSync(dir, { recursive: true });
    const write = (name: string, text: string) => {
      const tmp = join(dir, `.${name}.tmp`);
      writeFileSync(tmp, text);
      renameSync(tmp, join(dir, name));
    };
    return {
      dir,
      orders: (byPanel) => write('orders.json', JSON.stringify(byPanel)),
      panels: (list) => write('panels.json', JSON.stringify(list)),
      put: (name, content = '') => write(name, content),
      remove: (name) => rmSync(join(dir, name), { force: true }),
      starts: () => (existsSync(join(dir, 'starts')) ? readFileSync(join(dir, 'starts'), 'utf8').split('\n').filter(Boolean).length : 0),
    };
  }

  fakeGh() {
    const dir = join(this.home, 'fakegh');
    for (const d of ['calls', 'search', 'rest']) mkdirSync(join(dir, d), { recursive: true });
    return {
      dir,
      env: { FAKEGH_DIR: dir, PATH: `${fakeGhDir}:${process.env.PATH ?? ''}` },
      token: (user: string, value: string) => writeFileSync(join(dir, user === '' ? 'token' : `token-${user}`), value + '\n'),
    };
  }

  lanUrl = '';
  token = '';
  private lan = false;
  private listen = '127.0.0.1:0';
  private extraEnv: Record<string, string> = {};

  start(opts: { lan?: boolean; env?: Record<string, string> } = {}) {
    this.lan = !!opts.lan;
    this.extraEnv = opts.env ?? {};
    this.listen = this.lan ? '0.0.0.0:0' : '127.0.0.1:0';
    return this.resumeServer();
  }

  resumeServer() {
    return new Promise<void>((done, fail) => {
      // A harness identity variable from the engineer's shell must not reach the server.
      const env: NodeJS.ProcessEnv = { ...process.env, ...this.extraEnv, ADMIRAL_HOME: this.home };
      for (const v of ['CLAUDE_CONFIG_DIR', 'CODEX_HOME', 'COPILOT_HOME']) delete env[v];
      const proc = spawn(bin, ['--listen', this.listen], {
        env,
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      this.proc = proc;
      let out = '';
      let running = false;
      const timer = setTimeout(() => fail(new Error(`admiral did not print its URL: ${out}`)), 15_000);
      proc.once('error', fail);
      // An exit stopServer did not ask for fails the test: before the URL, the
      // start fails; after it, the test's open pages close and teardown reports it.
      proc.once('exit', (code, signal) => {
        const how = signal ? `was killed by ${signal}` : `exited with ${code}`;
        if (!running) return fail(new Error(`admiral ${how}: ${out}`));
        this.died = `admiral ${how} during the test`;
        this.onDeath?.();
      });
      const record = (b: Buffer) => {
        out += b.toString();
        this.log += b.toString();
      };
      proc.stdout!.on('data', (b: Buffer) => {
        record(b);
        const m = /Admiral is running at (http:\/\/127\.0\.0\.1:(\d+)\/)/.exec(out);
        if (running || !m) return;
        const other = /On other devices: (http:\/\/[\d.]+:\d+\/)\?token=([\w-]+)/.exec(out);
        if (this.lan && !other) return;
        clearTimeout(timer);
        running = true;
        this.url = m[1];
        // A restart comes back on the same port.
        this.listen = `${this.lan ? '0.0.0.0' : '127.0.0.1'}:${m[2]}`;
        if (other) [, this.lanUrl, this.token] = other;
        done();
      });
      proc.stderr!.on('data', record);
    });
  }

  async stopServer() {
    const proc = this.proc;
    if (proc && proc.exitCode === null && proc.signalCode === null) {
      proc.removeAllListeners('exit');
      const gone = new Promise((r) => proc.once('exit', r));
      proc.kill('SIGTERM');
      const hard = setTimeout(() => proc.kill('SIGKILL'), 3000);
      await gone;
      clearTimeout(hard);
    }
  }

  async restart() {
    await this.stopServer();
    await this.resumeServer();
  }

  rotateToken() {
    const token = randomBytes(24).toString('base64url');
    writeFileSync(join(this.home, 'token'), token + '\n', { mode: 0o600 });
    return token;
  }

  async stop() {
    await this.stopServer();
    // Admiral starts a tmux server that outlives it; end this home's.
    // Admiral is gone by now, so nothing starts a new one; wait until the socket stops answering.
    const sock = join(this.home, 'tmux.sock');
    spawnSync('tmux', ['-S', sock, 'kill-server']);
    for (let i = 0; i < 20 && spawnSync('tmux', ['-S', sock, 'list-sessions']).status === 0; i++) {
      await new Promise((r) => setTimeout(r, 100));
    }
    rmSync(this.home, { recursive: true, force: true });
  }
}

export const test = base.extend<{ admiral: Admiral }>({
  admiral: async ({ context }, use, info) => {
    const a = new Instance();
    // Close the test's pages so whatever it awaits fails now, not at its timeout.
    a.onDeath = () => void context.close().catch(() => {});
    try {
      await use(a);
    } finally {
      await a.stop();
      if (a.died || info.status !== info.expectedStatus) await info.attach('admiral server log', { body: a.log || '(no output)', contentType: 'text/plain' });
    }
    if (a.died) throw new Error(`${a.died}. Its output is attached as "admiral server log":\n${a.log.slice(-4000)}`);
  },
});

export { expect };
