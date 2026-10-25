import { type ChildProcess, spawn } from 'node:child_process';
import { appendFileSync, existsSync, mkdirSync, readFileSync, realpathSync, renameSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { type Admiral, expect } from './fixtures';

// Helpers for the chat scenarios (#119 u6). A Chat is one Admiral whose only fleet, "chat", carries the
// AdmiralBot session, so the cue file is its own: the shared World cue never fires SessionStart. The shapes
// follow the Go e2e (e2e/chat_session_test.go): the cue order, the transcript lines and the stdin bytes.
// Every line of text written here is made up.

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const fakeClaude = join(root, 'e2e', 'fakeharness', 'claude');
const bin = join(root, 'bin', 'admiral');

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  at: string;
}

export interface ChatSnapshot {
  running: boolean;
  state: string;
  messages: ChatMessage[];
  questions: { id: string; answer: unknown }[];
  error?: string;
}

/** A valid question as `admiral chat ask` reads it. */
export function askBody(title: string, opts: { multi?: boolean } = {}) {
  return {
    title,
    questions: [
      {
        id: '1',
        text: 'Which panel?',
        multi: !!opts.multi,
        options: [{ label: 'Freeform' }, { label: 'Work queue', description: 'the shared queue' }],
      },
    ],
  };
}

export class Chat {
  /** The real path of the temp ADMIRAL_HOME: the server resolves the Claude config dir with EvalSymlinks. */
  readonly root: string;
  readonly rec: string;
  readonly transcript: string;
  private readonly asks: ChildProcess[] = [];

  /**
   * Writes the config (fleet "chat", chat on sonnet) and the cue file. `after` are cue lines that follow
   * the first SessionStart, for example `wait go-work` and `event UserPromptSubmit {}`; `{transcript}` in
   * a line is the transcript's path.
   */
  constructor(
    readonly a: Admiral,
    after: string[] = [],
  ) {
    this.root = realpathSync(a.home);
    this.rec = join(this.root, 'rec');
    const cfg = join(this.root, 'id', 'chat-claude');
    this.transcript = join(cfg, 'projects', 'p', 't.jsonl');
    mkdirSync(join(cfg, 'projects', 'p'), { recursive: true });
    mkdirSync(join(this.root, 'work'), { recursive: true });
    mkdirSync(this.rec, { recursive: true });
    writeFileSync(this.transcript, '');
    const cue = join(this.root, 'cue-chat');
    writeFileSync(
      cue,
      [
        'record',
        'bracketedpaste',
        'wait go',
        `event SessionStart ${JSON.stringify({ session_id: 's1', hook_event_name: 'SessionStart', source: 'startup', transcript_path: this.transcript })}`,
        ...after.map((l) => l.replaceAll('{transcript}', this.transcript)),
      ].join('\n') + '\n',
    );
    a.writeConfig({
      version: 1,
      fleets: {
        chat: {
          name: 'Chat',
          search_roots: [join(this.root, 'work')],
          claude: {
            executable: fakeClaude,
            env: { FAKE_RECORD_DIR: this.rec, FAKE_CUE: cue, HOME: join(this.root, 'fh'), CLAUDE_CONFIG_DIR: cfg },
          },
        },
      },
      chat: { fleet: 'chat', model: 'sonnet' },
    });
  }

  private get dir() {
    return join(this.rec, '_chat');
  }

  async session(): Promise<ChatSnapshot> {
    return (await fetch(`${this.a.url}api/chat/session`)).json();
  }

  async post(path: string, body?: unknown) {
    const res = await fetch(`${this.a.url}${path}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return { status: res.status, text: await res.text() };
  }

  /** Releases a `wait <name>` cue of the fake. */
  release(name = 'go') {
    mkdirSync(this.dir, { recursive: true });
    writeFileSync(join(this.dir, name), '');
  }

  /** Starts the chat through the API, as opening it does, and waits for the fake to record. */
  async start() {
    const r = await this.post('api/chat/start');
    expect(r.status, `POST /api/chat/start: ${r.text}`).toBe(200);
    await expect.poll(() => existsSync(join(this.dir, 'recording')), { message: 'the fake of _chat is recording', timeout: 15_000 }).toBe(true);
  }

  /** start, then SessionStart: the chat is running with a transcript and no error. */
  async begin() {
    await this.start();
    this.release('go');
    await expect
      .poll(async () => {
        const s = await this.session();
        return s.running && !s.error;
      }, { message: 'the chat is running with a transcript', timeout: 15_000 })
      .toBe(true);
  }

  /** begin, then the lines, once the server serves them. */
  async beginWith(lines: { user?: string; assistant?: string }[]) {
    await this.begin();
    let n = 0;
    for (const l of lines) {
      if (l.user !== undefined) this.user(`u-${++n}`, l.user);
      if (l.assistant !== undefined) this.assistant(`a-${++n}`, l.assistant);
    }
    await expect.poll(async () => (await this.session()).messages.length, { message: 'the lines are served', timeout: 10_000 }).toBe(n);
  }

  user(uuid: string, text: string) {
    this.line({ type: 'user', uuid, timestamp: new Date().toISOString(), message: { role: 'user', content: text } });
  }

  assistant(uuid: string, text: string) {
    this.line({
      type: 'assistant',
      uuid,
      timestamp: new Date().toISOString(),
      message: { role: 'assistant', id: `msg_${uuid}`, content: [{ type: 'text', text }] },
    });
  }

  private line(v: unknown) {
    appendFileSync(this.transcript, JSON.stringify(v) + '\n');
  }

  /** The bytes the fake has read from its terminal since it began recording. */
  stdin() {
    const p = join(this.dir, 'stdin');
    return existsSync(p) ? readFileSync(p, 'latin1') : '';
  }

  /**
   * Runs the built `admiral chat ask` against this home with `body` on stdin and no ADMIRAL_SHIP. Resolves
   * to a handle: `out()` is what it has printed, `exited` resolves with its exit code. Nothing answers in
   * red, so `stop()` kills what is left.
   */
  ask(body: unknown) {
    const env: NodeJS.ProcessEnv = { PATH: process.env.PATH, HOME: join(this.root, 'fh'), ADMIRAL_HOME: this.a.home };
    const p = spawn(bin, ['chat', 'ask'], { env, stdio: ['pipe', 'pipe', 'pipe'] });
    this.asks.push(p);
    let out = '';
    p.stdout!.on('data', (b: Buffer) => (out += b.toString()));
    p.stderr!.on('data', (b: Buffer) => (out += b.toString()));
    p.stdin!.end(JSON.stringify(body));
    const exited = new Promise<number | null>((r) => p.once('exit', (code) => r(code)));
    return { out: () => out, exited };
  }

  /** Writes a question straight into chat/questions/, the way `admiral chat ask` would. */
  writeQuestion(id: string, body: ReturnType<typeof askBody>) {
    const dir = join(this.a.home, 'chat', 'questions');
    mkdirSync(dir, { recursive: true });
    const tmp = join(dir, `.${id}.json.tmp`);
    writeFileSync(tmp, JSON.stringify({ version: 1, id, kind: 'question', created: new Date().toISOString(), title: body.title, question: { questions: body.questions } }));
    renameSync(tmp, join(dir, `${id}.json`));
  }

  stop() {
    for (const p of this.asks) p.kill('SIGKILL');
  }

  /** The raw body of GET /api/inbox. */
  async inbox() {
    return (await fetch(`${this.a.url}api/inbox`)).text();
  }
}

/**
 * Resolves when the page has received its first `chat` snapshot. Call it before `page.goto`: a line written
 * before that snapshot is read counts as already seen, so it never sets the dot.
 */
export function firstChatSnapshot(page: Page): Promise<void> {
  return new Promise((resolve) => {
    page.on('websocket', (ws) =>
      ws.on('framereceived', (f) => {
        if (String(f.payload).includes('"type":"chat"')) resolve();
      }),
    );
  });
}
