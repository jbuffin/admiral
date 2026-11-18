import { expect, type Locator, type Page } from '@playwright/test';
import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Admiral } from './fixtures';
import { seedFleets } from './shell';

const chip = (label: string, tone?: string) => ({ label, ...(tone ? { tone } : {}) });
const action = { id: 'pass', label: 'Pass review', confirm: 'form', summary: 'Pass the review' };
// A module action id is unique within a row and its steps, so the step's differs from the row's.
const stepAction = { id: 'recheck', label: 'Recheck', confirm: 'click' };

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
  ],
};

const detail = (owner: string) => ({
  sections: [
    { kind: 'links', title: 'Links', links: [{ label: 'Ticket', url: 'https://tracker.example/t/1' }] },
    { kind: 'steps', title: 'Steps', steps: [{ id: 's1', title: 'Code review', chips: [chip('open', 'warn')], actions: [stepAction] }] },
    { kind: 'fields', title: 'Details', fields: [{ label: 'Owner', value: owner }] },
  ],
});

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

/** A module named Deploys in the Westwind fleet, served by the test plugin; the shell is seeded and Admiral is not yet started. */
export function seedModule(a: Admiral): Module {
  seedFleets(a);
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
  m.page(PAGE);
  m.row('t1', detail('amy'));
  const path = join(a.home, 'config.json');
  const config = JSON.parse(readFileSync(path, 'utf8'));
  config.sources = { deploys: { plugin: 'testplug', fleet: 'zulu', name: 'Deploys', settings: { flagdir: f.dir, capabilities: ['page'] } } };
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
