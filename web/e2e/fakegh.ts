import { mkdirSync, readFileSync, readdirSync, existsSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// The fake gh (e2e/testdata/fakegh/gh) for a browser test. Put `env()` into admiral.start({ env }) so the
// fake is first on Admiral's PATH, then can() what GitHub answers and read what gh was asked from calls().

const fakeDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'testdata', 'fakegh');

export interface Call {
  method: string;
  endpoint: string;
  /** What `--input -` read: for a review's submit, the JSON `{"event","body"}`. Empty when none. */
  stdin: string;
}

export class FakeGh {
  readonly dir: string;

  constructor(base: string) {
    this.dir = join(base, 'fakegh');
    mkdirSync(join(this.dir, 'rest'), { recursive: true });
    mkdirSync(join(this.dir, 'calls'), { recursive: true });
  }

  /** The environment that puts the fake first on PATH. */
  env(): Record<string, string> {
    return { PATH: `${fakeDir}:${process.env.PATH}`, FAKEGH_DIR: this.dir };
  }

  /** A canned REST answer. A later can() for the same method and endpoint replaces the earlier one. */
  can(method: string, endpoint: string, answer: { status?: number; json?: unknown; stderr?: string } = {}) {
    const key = join(this.dir, 'rest', `${method.toLowerCase()}-${endpoint.replace(/[^A-Za-z0-9]/g, '_')}`);
    writeFileSync(`${key}.endpoint`, endpoint);
    writeFileSync(`${key}.method`, method);
    writeFileSync(`${key}.status`, String(answer.status ?? 200));
    writeFileSync(`${key}.json`, JSON.stringify(answer.json ?? {}));
    if (answer.stderr !== undefined) writeFileSync(`${key}.stderr`, answer.stderr);
  }

  /** The `gh api` calls so far, oldest first. */
  calls(): Call[] {
    const root = join(this.dir, 'calls');
    return readdirSync(root)
      .sort()
      .map((d) => join(root, d))
      .filter((d) => existsSync(join(d, 'endpoint')))
      .map((d) => ({
        method: readFileSync(join(d, 'method'), 'utf8').trim(),
        endpoint: readFileSync(join(d, 'endpoint'), 'utf8').trim(),
        stdin: existsSync(join(d, 'stdin')) ? readFileSync(join(d, 'stdin'), 'utf8') : '',
      }));
  }
}
