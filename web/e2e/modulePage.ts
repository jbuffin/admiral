import { expect, type Locator, type Page } from '@playwright/test';
import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Admiral } from './fixtures';
import { seedFleets } from './shell';

export const MESSAGE_JOB = 'Please review this change.';
export const MESSAGE_LINK = 'https://tracker.example/t/1';
/** The built-in actions on row t1 when a module is seeded with them: Open link, Launch on an issue and the AdmiralBot message. */
export const BUILTINS = [
  { id: 'docs', label: 'Open docs', builtin: 'open', url: 'https://tracker.example/docs' },
  { id: 'dock', label: 'Start ship', builtin: 'launch', url: 'https://github.com/acme/widgets/issues/7' },
  { id: 'tell', label: 'Message AdmiralBot', builtin: 'message', kind: 'review', ref: 'T-1', title: 'Fix login', link: MESSAGE_LINK },
];

const chip = (label: string, tone?: string) => ({ label, ...(tone ? { tone } : {}) });
const action = { id: 'pass', label: 'Pass review', confirm: 'form', summary: 'Pass the review' };
// A module action id is unique within a row and its steps, so the step's differs from the row's.
const stepAction = { id: 'recheck', label: 'Recheck', confirm: 'click' };

// A row aimed at one stage: Rerun stage acts on it, Skip stage on another, Cancel run on the whole run.
const rerun = { id: 'rerun', label: 'Rerun stage', confirm: 'click', step: 'deploy' };
const skip = { id: 'skip', label: 'Skip stage', confirm: 'click', step: 'smoke' };
const cancel = { id: 'cancel', label: 'Cancel run', confirm: 'click' };

export const PAGE = {
  waiting: 2,
  groups: [
    {
      id: 'review',
      title: 'Needs review',
      rows: [
        { id: 't1', title: 'Fix login', subtitle: 'Payments team', chips: [chip('P1', 'warn')], reason: 'Review is open', action },
        { id: 't2', title: 'Rotate keys', subtitle: '', chips: [chip('security', 'good')], reason: '' },
      ],
    },
    { id: 'done', title: 'Done', rows: [{ id: 't3', title: 'Ship docs', subtitle: 'Docs', chips: [chip('closed', 'muted')], reason: 'Merged last week' }] },
    {
      id: 'pipeline',
      title: 'Pipeline',
      rows: [
        {
          id: 't4',
          title: 'Release build',
          subtitle: 'Run 41',
          chips: [chip('main', 'neutral')],
          reason: 'Deploy stage failed',
          target: { label: 'Deploy stage', chips: [chip('Failed', 'warn')], step: 'deploy' },
          action: rerun,
          actions: [skip, cancel],
        },
      ],
    },
  ],
};

const detail = (owner: string) => ({
  sections: [
    { kind: 'links', title: 'Links', links: [{ label: 'Ticket', url: 'https://tracker.example/t/1' }] },
    { kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [chip('open', 'warn')], actions: [stepAction] }] },
    { kind: 'fields', title: 'Details', fields: [{ label: 'Owner', value: owner }] },
  ],
});

/** The detail of row t4: three stages and two commit links with chips and a one-line title. */
export const pipelineDetail = {
  sections: [
    {
      kind: 'links',
      title: 'Commits',
      links: [
        { label: 'a1b2c3d', url: 'https://ci.example/commit/a1b2c3d', subtitle: 'Raise the retry limit', chips: [chip('Merged', 'good')] },
        { label: 'e4f5a6b', url: 'https://ci.example/commit/e4f5a6b', subtitle: 'Fix the cache key', chips: [chip('Open', 'warn'), chip('2 reviews', 'muted')] },
      ],
    },
    {
      kind: 'steps',
      title: 'Stages',
      steps: [
        { id: 'build', title: 'Build', subtitle: 'Finished in 3 min', chips: [chip('Passed', 'good')], actions: [] },
        { id: 'smoke', title: 'Smoke test', subtitle: 'Waiting for the deploy', chips: [chip('Pending', 'muted')], actions: [] },
        { id: 'deploy', title: 'Deploy', subtitle: 'Timed out talking to the registry', chips: [chip('Failed', 'warn')], actions: [{ id: 'logs', label: 'Fetch logs', confirm: 'click' }] },
      ],
    },
  ],
};

export interface Module {
  /** Writes page.json atomically-enough for the plugin's per-request read. */
  page(p: unknown): void;
  row(id: string, detail: unknown): void;
  /** The action.form answer for a module action id. */
  form(id: string, form: unknown): void;
  /** The action.run answer for a module action id; an object with `error` answers an error and `delay` waits that many seconds first. */
  run(id: string, answer: unknown): void;
  /** Has the plugin send page.changed, naming rows when given. */
  changed(rows?: string[]): void;
}

/** A module named Deploys in the Westwind fleet (the Harborlight fleet, with a running-capable AdmiralBot chat, when `chat`), served by the test plugin; the shell is seeded and Admiral is not yet started. */
export function seedModule(a: Admiral, opts: { chat?: boolean } = {}): Module {
  seedFleets(a, { chat: opts.chat });
  a.installTestPlugin();
  const f = a.flags('deploys');
  for (const d of ['rows', 'forms', 'runs']) mkdirSync(join(f.dir, d), { recursive: true });
  // Flags.put writes only at the top of the flag directory.
  const put = (dir: string, id: string, d: unknown) => {
    const tmp = join(f.dir, dir, `.${id}.tmp`);
    writeFileSync(tmp, JSON.stringify(d));
    renameSync(tmp, join(f.dir, dir, `${id}.json`));
  };
  const m: Module = {
    page: (p) => f.put('page.json', JSON.stringify(p)),
    row: (id, d) => put('rows', id, d),
    form: (id, d) => put('forms', id, d),
    run: (id, d) => put('runs', id, d),
    changed: (rows) => f.put('do-page-changed', rows ? JSON.stringify({ rows }) : ''),
  };
  if (opts.chat) {
    // Admiral reads the kinds only at initialize, so they are written before the source starts.
    f.put('message-kinds.json', JSON.stringify([{ kind: 'review', label: 'Review', job: MESSAGE_JOB }]));
    const [first, ...rest] = PAGE.groups[0].rows;
    m.page({ ...PAGE, groups: [{ ...PAGE.groups[0], rows: [{ ...first, actions: BUILTINS }, ...rest] }, ...PAGE.groups.slice(1)] });
  } else m.page(PAGE);
  m.row('t1', detail('amy'));
  m.row('t4', pipelineDetail);
  const path = join(a.home, 'config.json');
  const config = JSON.parse(readFileSync(path, 'utf8'));
  config.sources = { deploys: { plugin: 'testplug', fleet: opts.chat ? 'harbor' : 'zulu', name: 'Deploys', settings: { flagdir: f.dir, capabilities: ['page'] } } };
  a.writeConfig(config);
  return m;
}

export { detail as rowDetail };

/** The module's entry in the main navigation (the sidebar, or the More sheet on a phone). */
export const entry = (scope: Locator) => scope.getByRole('link', { name: /^Deploys/ });

export async function expectRowsDrawn(page: Page) {
  const main = page.getByRole('main');
  await expect(main.getByRole('heading', { name: /^Needs review/ })).toBeVisible();
  await expect(main.getByRole('heading', { name: /^Done/ })).toBeVisible();
  const fix = main.getByRole('listitem').filter({ hasText: 'Fix login' });
  await expect(fix).toContainText('Payments team');
  await expect(fix).toContainText('P1');
  await expect(fix).toContainText('Review is open');
}

const LONG_ACTION = { id: 'long', label: 'Pick files', confirm: 'form' };
const LONG_OPTIONS = 48;

/** Row t1 gains a form action whose checklist is far taller than any viewport; `actions` are the row's other module actions. */
export function seedLongForm(mod: Module, actions: unknown[]) {
  mod.form('long', {
    title: 'Pick the files to deploy',
    fields: [
      { id: 'whose', label: 'Whose work', type: 'choice', options: [{ id: 'a', label: 'Amy' }, { id: 'b', label: 'Bo' }], default: 'b' },
      { id: 'files', label: 'Files', type: 'checklist', default: [], options: Array.from({ length: LONG_OPTIONS }, (_, i) => ({ id: `f${i}`, label: `File number ${i + 1}` })) },
      { id: 'time', label: 'Time', type: 'duration', default: '01:30', min: '00:15' },
    ],
  });
  const [first, ...rest] = PAGE.groups[0].rows;
  mod.page({ ...PAGE, groups: [{ ...PAGE.groups[0], rows: [{ ...first, actions: [...actions, LONG_ACTION] }, ...rest] }, ...PAGE.groups.slice(1)] });
}

/** The first thing under a sheet's title is fully visible: nothing sticks over it. */
export async function expectBelowTitle(sheet: Locator, title: string, first: Locator) {
  const head = (await sheet.getByRole('heading', { name: title }).boundingBox())!;
  const b = (await first.boundingBox())!;
  expect(b.y).toBeGreaterThanOrEqual(head.y + head.height);
}

/** Opens the long form from row t1, then checks the sheet fits the viewport and its content scrolls inside it. */
export async function expectLongSheetFitsAndScrolls(page: Page) {
  const fix = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Fix login' });
  await fix.getByRole('button', { name: LONG_ACTION.label }).tap();
  const sheet = page.getByRole('dialog', { name: 'Pick the files to deploy' });
  const last = sheet.getByRole('checkbox', { name: `File number ${LONG_OPTIONS}`, exact: true });
  await expect(sheet.getByRole('checkbox', { name: 'File number 1', exact: true })).toBeVisible();

  const view = page.viewportSize()!;
  const title = sheet.getByRole('heading', { name: 'Pick the files to deploy' });
  const cancel = sheet.getByRole('button', { name: 'Cancel', exact: true });
  const submit = sheet.getByRole('button', { name: 'Submit', exact: true });
  const within = async (l: Locator) => {
    const b = (await l.boundingBox())!;
    return b.y >= 0 && b.y + b.height <= view.height;
  };
  await expect.poll(async () => {
    const b = (await sheet.boundingBox())!;
    return b.y >= 8 && b.y + b.height <= view.height;
  }).toBe(true);
  for (const l of [title, cancel, submit]) await expect.poll(() => within(l)).toBe(true);
  await expectBelowTitle(sheet, 'Pick the files to deploy', sheet.getByText('Whose work', { exact: true }));

  // An option counts as in view only between the title's bottom and the foot's top, where the sticky parts don't cover it.
  const seen = async (l: Locator) => {
    const b = await l.boundingBox();
    const t = (await title.boundingBox())!;
    const f = (await cancel.boundingBox())!;
    return !!b && b.y >= t.y + t.height && b.y + b.height <= f.y;
  };
  expect(await seen(last)).toBe(false);
  await last.scrollIntoViewIfNeeded();
  await expect.poll(() => seen(last)).toBe(true);
  await last.tap();
  await expect(last).toBeChecked();

  for (const l of [title, cancel, submit]) expect(await within(l)).toBe(true);
  expect(await page.evaluate(() => document.scrollingElement!.scrollTop)).toBe(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(view.width);

  await cancel.tap();
  await expect(sheet).toBeHidden();
}
