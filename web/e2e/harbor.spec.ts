import type { Locator, Page } from '@playwright/test';
import { type Admiral, expect, test } from './fixtures';

const identityVar = { claude: 'CLAUDE_CONFIG_DIR', codex: 'CODEX_HOME', copilot: 'COPILOT_HOME' } as const;
type Harness = keyof typeof identityVar;

// A dock with a fake executable and its own identity directory under the temp
// home, so no two docks warn about a shared sign-in unless a test says so.
function dock(a: Admiral, fleet: string, harness: Harness, executable = a.fakeHarness(harness)) {
  return { executable, env: { [identityVar[harness]]: `${a.home}/identity/${fleet}-${harness}` } };
}

interface FleetSpec {
  name: string;
  searchRoots?: string[];
  docks: [Harness, ReturnType<typeof dock>][]; // in file order
}

function config(a: Admiral, fleets: Record<string, FleetSpec>) {
  const out: Record<string, unknown> = {};
  for (const [key, f] of Object.entries(fleets)) {
    out[key] = {
      name: f.name,
      search_roots: f.searchRoots ?? [a.home],
      ...Object.fromEntries(f.docks),
    };
  }
  return { version: 1, fleets: out };
}

const workFleet = (a: Admiral): FleetSpec => ({
  name: 'Work',
  docks: [['codex', dock(a, 'work', 'codex')], ['claude', dock(a, 'work', 'claude')]],
});
const homeFleet = (a: Admiral): FleetSpec => ({
  name: 'Home',
  docks: [
    ['codex', dock(a, 'home', 'codex')],
    ['copilot', dock(a, 'home', 'copilot')],
    ['claude', dock(a, 'home', 'claude')],
  ],
});

// Keys sort the other way round from file order, so a UI that sorts fails.
const twoFleets = (a: Admiral) => config(a, { zulu: workFleet(a), alpha: homeFleet(a) });

const marinas = (page: Page) => page.getByRole('region', { name: / marina$/ });
const marina = (page: Page, fleetName: string) => page.getByRole('region', { name: `${fleetName} marina` });
const docksIn = (m: Locator) => m.getByRole('button', { name: / dock, / });
const harnessNames = (m: Locator) =>
  docksIn(m).evaluateAll((els) => els.map((e) => (e.getAttribute('aria-label') ?? '').split(' dock, ')[0]));
const fleetNames = (page: Page) =>
  marinas(page).evaluateAll((els) => els.map((e) => (e.getAttribute('aria-label') ?? '').replace(/ marina$/, '')));
const dockNamed = (m: Locator, harness: string) => m.getByRole('button', { name: new RegExp(`^${harness} dock, `) });
// A mark's full text lives in the dock's accessible name or its title (brief item 6).
const dockText = (d: Locator) =>
  d.evaluate((e) => `${e.getAttribute('aria-label') ?? ''}\n${e.getAttribute('title') ?? ''}\n${e.textContent ?? ''}`);

async function serverFleets(page: Page, a: Admiral) {
  const res = await page.request.get(`${a.url}api/config`);
  return (await res.json()).fleets as { key: string; docks: { harness: string }[] }[];
}

test.describe('S1: marinas and docks from config', () => {
  test('two fleets show in config order, docks in claude, copilot, codex order, and the count', async ({ page, admiral }) => {
    admiral.writeConfig(twoFleets(admiral));
    await admiral.start();
    // Guard: the server sent what the page should draw, so a failure below is the page.
    expect((await serverFleets(page, admiral)).map((f) => f.key)).toEqual(['zulu', 'alpha']);

    await page.goto(admiral.url);

    await expect(page.getByRole('banner').getByText('2 fleets')).toBeVisible();
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Home']);
    await expect.poll(() => harnessNames(marina(page, 'Work'))).toEqual(['Claude', 'Codex']);
    await expect.poll(() => harnessNames(marina(page, 'Home'))).toEqual(['Claude', 'Copilot', 'Codex']);

    // The sign names the fleet and shows its key beside it.
    await expect(marina(page, 'Work').getByText('zulu', { exact: true })).toBeVisible();
    await expect(marina(page, 'Home').getByText('alpha', { exact: true })).toBeVisible();

    // Healthy docks sit in port.
    await expect(marina(page, 'Home').getByText('All in port')).toHaveCount(3);
  });
});

test.describe('S2: editing config.json updates the page live', () => {
  test('add a fleet, break the file, fix it: marinas and banner follow with no reload', async ({ page, admiral }) => {
    admiral.writeConfig(config(admiral, { zulu: workFleet(admiral) }));
    await admiral.start();
    await page.goto(admiral.url);
    await expect(page.getByRole('banner').getByText('1 fleet', { exact: true })).toBeVisible();
    await expect.poll(() => fleetNames(page)).toEqual(['Work']);
    await page.evaluate(() => ((window as unknown as { noReload: boolean }).noReload = true));

    admiral.writeConfig(twoFleets(admiral));
    await expect(page.getByRole('banner').getByText('2 fleets')).toBeVisible();
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Home']);

    // A bad key: the banner names the file and the error, and the last good config stays.
    admiral.writeConfigText(JSON.stringify({ version: 1, fleets: { zulu: { name: 'Work', claud: {} } } }));
    await expect(page.getByText('config.json').first()).toBeVisible();
    await expect(page.getByText('fleets.zulu.claud').first()).toBeVisible();
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Home']);
    await expect(page.getByRole('banner').getByText('2 fleets')).toBeVisible();

    admiral.writeConfig(twoFleets(admiral));
    await expect(page.getByText('fleets.zulu.claud')).toHaveCount(0);
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Home']);

    expect(await page.evaluate(() => (window as unknown as { noReload?: boolean }).noReload)).toBe(true);
  });
});

test.describe('S3: no fleets', () => {
  test('no config shows one empty "Set up a fleet" marina, then gives way to a real fleet', async ({ page, admiral }) => {
    await admiral.start();
    await page.goto(admiral.url);

    await expect(page.getByText('Set up a fleet')).toHaveCount(1);
    await expect(page.getByText('Set up a fleet')).toBeVisible();
    await expect(docksIn(page.locator('body'))).toHaveCount(0);

    admiral.writeConfig(config(admiral, { zulu: workFleet(admiral) }));
    await expect.poll(() => fleetNames(page)).toEqual(['Work']);
    await expect(page.getByText('Set up a fleet')).toHaveCount(0);
  });

  test('an invalid config at startup shows the banner and "Set up a fleet" together', async ({ page, admiral }) => {
    admiral.writeConfigText(JSON.stringify({ version: 1, fleets: { zulu: { name: 'Work', claud: {} } } }));
    await admiral.start();
    await page.goto(admiral.url);

    await expect(page.getByText('fleets.zulu.claud').first()).toBeVisible();
    await expect(page.getByText('config.json').first()).toBeVisible();
    await expect(page.getByText('Set up a fleet')).toBeVisible();
  });
});

test.describe('S4: layout fits', () => {
  test('ipad-landscape: header, sidebar, open water and land all fit with no scroll', async ({ page, admiral }, info) => {
    test.skip(info.project.name !== 'ipad-landscape');
    admiral.writeConfig(twoFleets(admiral));
    await admiral.start();
    await page.goto(admiral.url);
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Home']);

    const { width, height } = page.viewportSize()!;
    const zones = {
      header: page.getByRole('banner'),
      sidebar: page.getByRole('complementary', { name: 'Orders' }),
      water: page.getByRole('main', { name: 'Open water' }),
      land: page.getByLabel('Marinas', { exact: true }),
    };
    for (const [name, zone] of Object.entries(zones)) {
      await expect(zone, name).toBeVisible();
      const b = (await zone.boundingBox())!;
      expect(b.x, `${name} left`).toBeGreaterThanOrEqual(0);
      expect(b.y, `${name} top`).toBeGreaterThanOrEqual(0);
      expect(b.x + b.width, `${name} right`).toBeLessThanOrEqual(width + 0.5);
      expect(b.y + b.height, `${name} bottom`).toBeLessThanOrEqual(height + 0.5);
    }
    for (const m of [marina(page, 'Work'), marina(page, 'Home')]) {
      const b = (await m.boundingBox())!;
      expect(b.y + b.height, 'marina bottom').toBeLessThanOrEqual(height + 0.5);
    }
    const scroll = await page.evaluate(() => ({
      w: document.documentElement.scrollWidth - innerWidth,
      h: document.documentElement.scrollHeight - innerHeight,
    }));
    expect(scroll.w, 'horizontal scroll').toBeLessThanOrEqual(0);
    expect(scroll.h, 'vertical scroll').toBeLessThanOrEqual(0);
  });

  test('ipad-portrait: the zones wrap and nothing scrolls sideways', async ({ page, admiral }, info) => {
    test.skip(info.project.name !== 'ipad-portrait');
    admiral.writeConfig(twoFleets(admiral));
    await admiral.start();
    await page.goto(admiral.url);
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Home']);

    const { width } = page.viewportSize()!;
    for (const zone of [page.getByRole('main', { name: 'Open water' }), page.getByLabel('Marinas', { exact: true })]) {
      await expect(zone).toBeVisible();
      const b = (await zone.boundingBox())!;
      expect(b.x + b.width).toBeLessThanOrEqual(width + 0.5);
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
    expect(overflow, 'horizontal scroll').toBeLessThanOrEqual(0);
  });
});

test.describe('S5: dock and fleet marks from config', () => {
  test('a missing executable, no search roots and a shared identity are marked on the docks', async ({ page, admiral }) => {
    const missing = admiral.missingHarness('codex');
    const shared = `${admiral.home}/identity/shared`;
    admiral.writeConfig(
      config(admiral, {
        work: {
          name: 'Work',
          docks: [['claude', dock(admiral, 'work', 'claude')], ['codex', dock(admiral, 'work', 'codex', missing)]],
        },
        bare: { name: 'Bare', searchRoots: [], docks: [['claude', dock(admiral, 'bare', 'claude')]] },
        one: {
          name: 'One',
          docks: [['claude', { executable: admiral.fakeHarness('claude'), env: { CLAUDE_CONFIG_DIR: shared } }]],
        },
        two: {
          name: 'Two',
          docks: [['claude', { executable: admiral.fakeHarness('claude'), env: { CLAUDE_CONFIG_DIR: shared } }]],
        },
      }),
    );
    await admiral.start();
    // Guard: the server marks what the page should show.
    const sent = (await (await page.request.get(`${admiral.url}api/config`)).json()).fleets;
    expect(sent.find((f: { key: string }) => f.key === 'work').docks[1].executable_found).toBe(false);
    expect(sent.find((f: { key: string }) => f.key === 'bare').can_launch).toBe(false);

    await page.goto(admiral.url);
    await expect.poll(() => fleetNames(page)).toEqual(['Work', 'Bare', 'One', 'Two']);

    const work = marina(page, 'Work');
    // Positive control: the healthy dock still reads "All in port".
    await expect(work.getByText('All in port')).toHaveCount(1);
    await expect(dockNamed(work, 'Claude').getByText('All in port')).toBeVisible();

    // Missing executable: the status line is no longer "All in port", and the
    // mark carries the path.
    const codex = dockNamed(work, 'Codex');
    await expect(codex).toBeVisible();
    await expect(codex.getByText('All in port')).toHaveCount(0);
    expect(await dockText(codex)).toContain(missing);

    // No search roots: visible on each of the fleet's docks.
    const bare = marina(page, 'Bare');
    await expect(docksIn(bare)).toHaveCount(1);
    await expect(bare.getByText('No search roots')).toBeVisible();
    await expect(bare.getByText('All in port')).toHaveCount(0);

    // Shared identity: the server's full text on both docks.
    for (const [name, other, otherKey] of [['One', 'Two', 'two'], ['Two', 'One', 'one']]) {
      const d = dockNamed(marina(page, name), 'Claude');
      await expect(d.getByText('All in port')).toHaveCount(0);
      expect(await dockText(d)).toContain(`Shares its sign-in directory ${shared} with fleet ${other} (${otherKey})`);
    }
  });
});
