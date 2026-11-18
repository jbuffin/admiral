// A module's nav entries, its paths and how the router treats them before and after the sources load.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const page = vi.hoisted(() => {
  const l = { pathname: '/app/inbox', search: '' };
  const install = () => {
    vi.stubGlobal('location', l);
    vi.stubGlobal('addEventListener', () => {});
    const set = (_s: unknown, _t: string, url: string) => {
      const [pathname, search = ''] = url.split('?');
      l.pathname = pathname;
      l.search = search ? `?${search}` : '';
    };
    vi.stubGlobal('history', { replaceState: set, pushState: set });
  };
  install();
  return Object.assign(l, { install });
});

import { effect } from '@preact/signals';
import type { Source } from './api';
import { resolveModule } from './modules';
import { go, loc, moduleOf, modulePath, pathAfterFleetSwitch, provideFleets, settleRoute, settleTarget, startRouter, surfaceOf } from './router';
import { sources, sourcesLoaded } from './shipsLink';
import { isModule, moreItems, navItems } from './surfaces';

const src = (key: string, over: Partial<Source> = {}): Source => ({ key, name: key.toUpperCase(), fleet: 'zulu', views: [], ...over }) as Source;
const mod = (key: string, over: Partial<Source> = {}) => src(key, { icon_url: `/api/plugins/${key}/icon`, capabilities: ['page'], page: { state: 'ready', waiting: 0, version: 1 }, ...over });

const ids = (items: ReturnType<typeof navItems>) => items.map((i) => (i.kind === 'surface' ? i.id : `module:${i.key}`));

describe('what counts as a module', () => {
  it('is a source with a page summary or one that lists page among its capabilities', () => {
    expect(isModule(src('a', { page: { state: 'starting', waiting: 0, version: 0 } }))).toBe(true);
    expect(isModule(src('b', { capabilities: ['refresh', 'page'] }))).toBe(true);
    expect(isModule(src('c', { capabilities: ['refresh'] }))).toBe(false);
    expect(isModule(src('d'))).toBe(false);
  });
});

describe('the desktop nav', () => {
  it('puts the active fleet\'s modules after Crew and before Preferences', () => {
    const items = navItems([mod('deploys'), mod('deploys')], 'zulu');
    expect(ids(items)).toEqual(['inbox', 'ships', 'orders', 'chat', 'campaigns', 'routines', 'crew', 'module:deploys', 'module:deploys', 'preferences']);
  });

  it('carries the source name, icon and waiting count of each module', () => {
    const items = navItems([mod('deploys', { name: 'Deploys', page: { state: 'ready', waiting: 3, version: 2 } })], 'zulu');
    expect(items.find((i) => i.kind === 'module')).toMatchObject({ key: 'deploys', name: 'Deploys', icon_url: '/api/plugins/deploys/icon', waiting: 3 });
  });

  it('leaves out another fleet\'s module and a source that is not a module', () => {
    const items = navItems([mod('deploys', { fleet: 'yankee' }), src('github'), src('plain', { capabilities: ['refresh'] })], 'zulu');
    expect(ids(items)).toEqual(['inbox', 'ships', 'orders', 'chat', 'campaigns', 'routines', 'crew', 'preferences']);
  });

  it('gives a source named like a built-in surface no entry', () => {
    const items = navItems([mod('orders'), mod('inbox'), mod('crew'), mod('preferences'), mod('deploys')], 'zulu');
    expect(ids(items).filter((i) => i.startsWith('module:'))).toEqual(['module:deploys']);
    expect(ids(items).filter((i) => i === 'orders')).toHaveLength(1);
  });

  it('shows a module that has no page summary yet when it lists the capability', () => {
    expect(ids(navItems([src('deploys', { capabilities: ['page'] })], 'zulu'))).toContain('module:deploys');
  });
});

describe('the phone nav', () => {
  it('lists a module under More, after Crew and before Preferences, and not as a tab', () => {
    expect(ids(moreItems([mod('deploys')], 'zulu'))).toEqual(['campaigns', 'routines', 'crew', 'module:deploys', 'preferences']);
  });

  it('leaves out another fleet\'s module', () => {
    expect(ids(moreItems([mod('deploys', { fleet: 'yankee' })], 'zulu'))).toEqual(['campaigns', 'routines', 'crew', 'preferences']);
  });
});

describe('module paths', () => {
  it('open the page for a source key, and a row for a key and a row id, encoding both', () => {
    expect(modulePath('deploys')).toBe('/app/deploys');
    expect(modulePath('deploys', '42')).toBe('/app/deploys/42');
    expect(moduleOf('/app/deploys')).toEqual({ key: 'deploys', row: null });
    expect(moduleOf('/app/deploys/42/')).toEqual({ key: 'deploys', row: '42' });
    expect(moduleOf(modulePath('my source', 'a/b c#1'))).toEqual({ key: 'my source', row: 'a/b c#1' });
    expect(modulePath('my source', 'a/b c#1')).toBe('/app/my%20source/a%2Fb%20c%231');
  });

  it('name no module for a row id that is a dot segment', () => {
    expect(moduleOf('/app/deploys/..')).toBeNull();
    expect(moduleOf('/app/deploys/.')).toBeNull();
    expect(moduleOf('/app/deploys/%2E%2E')).toBeNull();
    expect(moduleOf('/app/deploys/a..b')).toEqual({ key: 'deploys', row: 'a..b' });
  });

  it('name no module on the root, a path below a row or a malformed escape', () => {
    expect(moduleOf('/app')).toBeNull();
    expect(moduleOf('/app/')).toBeNull();
    expect(moduleOf('/app/deploys/1/extra')).toBeNull();
    expect(moduleOf('/app/deploys/%E0%A4%A')).toBeNull();
  });

  it('are not surfaces', () => {
    expect(surfaceOf('/app/deploys')).toBeUndefined();
  });
});

describe('opening a module path', () => {
  it('waits while the sources have not loaded', () => {
    expect(resolveModule('/app/deploys/7', [], false)).toEqual({ state: 'pending' });
  });

  it('opens the page of a module of the fleet, with the selected row', () => {
    expect(resolveModule('/app/deploys/7', [mod('deploys')], true)).toMatchObject({ state: 'open', fleet: 'zulu', row: '7', source: { key: 'deploys' } });
  });

  it('names the other fleet when the module belongs to another, so the shell switches to it', () => {
    expect(resolveModule('/app/deploys', [mod('deploys', { fleet: 'yankee' })], true)).toMatchObject({ state: 'open', fleet: 'yankee' });
  });

  it('is unknown for a key no module has and a non-module source, once loaded', () => {
    expect(resolveModule('/app/nope', [mod('deploys')], true)).toEqual({ state: 'unknown' });
    expect(resolveModule('/app/plain', [src('plain')], true)).toEqual({ state: 'unknown' });
  });

  it('is nothing for a built-in surface id, with a row or not, loaded or not', () => {
    expect(resolveModule('/app/orders/9', [mod('orders')], true)).toBeNull();
    expect(resolveModule('/app/orders/9', [], false)).toBeNull();
  });

  it('waits while a source of that key is still starting, since it lists no capabilities yet', () => {
    const starting = src('deploys', { state: 'starting', capabilities: [] });
    expect(resolveModule('/app/deploys', [starting], true)).toEqual({ state: 'pending' });
    expect(resolveModule('/app/deploys', [mod('deploys')], true)).toMatchObject({ state: 'open' });
    expect(resolveModule('/app/deploys', [src('deploys', { state: 'error' })], true)).toEqual({ state: 'unknown' });
  });

  it('is nothing for a path that is not a module path', () => {
    expect(resolveModule('/app/crew', [mod('deploys')], true)).toBeNull();
  });
});

describe('the router around module paths', () => {
  beforeEach(() => {
    page.install();
    page.search = '';
    sources.value = [];
    sourcesLoaded.value = false;
  });
  afterEach(() => {
    sources.value = [];
    sourcesLoaded.value = false;
    loc.value = { path: '/app/inbox', search: '' };
    provideFleets(() => ({ active: null, keys: [] }));
  });

  it('keeps a module path on a first render before the sources have arrived', () => {
    page.pathname = '/app/deploys/7';
    startRouter();
    expect(page.pathname).toBe('/app/deploys/7');
    expect(loc.value.path).toBe('/app/deploys/7');
  });

  it('keeps the path once the sources arrive with that module', () => {
    page.pathname = '/app/deploys/7';
    startRouter();
    sources.value = [mod('deploys')];
    sourcesLoaded.value = true;
    settleRoute();
    expect(page.pathname).toBe('/app/deploys/7');
  });

  it('falls back to the inbox once the sources have loaded without that key', () => {
    page.pathname = '/app/nope';
    startRouter();
    expect(page.pathname).toBe('/app/nope');
    sources.value = [mod('deploys')];
    sourcesLoaded.value = true;
    settleRoute();
    expect(page.pathname).toBe('/app/inbox');
    expect(loc.value.path).toBe('/app/inbox');
  });

  it('still sends an unknown path with two segments to the inbox at once when the sources have loaded', () => {
    sourcesLoaded.value = true;
    page.pathname = '/app/nope/deep/er';
    startRouter();
    expect(page.pathname).toBe('/app/inbox');
  });
});

describe('the fleet a module path opens', () => {
  const fleets = { active: 'zulu', keys: ['zulu', 'yankee'] };
  const other = [mod('deploys', { fleet: 'yankee' })];

  it('replaces the entry with the module\'s fleet for a deep link to another fleet\'s module', () => {
    expect(settleTarget('/app/deploys/7', '?fleet=zulu', other, true, fleets)).toEqual({ path: '/app/deploys/7', search: '?fleet=yankee' });
    expect(settleTarget('/app/deploys', '', other, true, fleets)).toEqual({ path: '/app/deploys', search: '?fleet=yankee' });
  });

  it('settles once: after the switch the same path stays', () => {
    expect(settleTarget('/app/deploys', '?fleet=yankee', other, true, { ...fleets, active: 'yankee' })).toBeNull();
  });

  it('keeps a link to the active fleet\'s module, and one whose fleet is not known', () => {
    expect(settleTarget('/app/deploys', '?fleet=zulu', [mod('deploys')], true, fleets)).toBeNull();
    expect(settleTarget('/app/deploys', '', other, true, { active: 'zulu', keys: ['zulu'] })).toBeNull();
    expect(settleTarget('/app/deploys', '', other, true, { active: null, keys: [] })).toBeNull();
  });

  it('leaves the module page for the inbox when the engineer switches fleet away from it', () => {
    const list = [mod('deploys')];
    expect(pathAfterFleetSwitch('/app/deploys/7', '?fleet=zulu', 'yankee', list, true)).toBe('/app/inbox');
    expect(pathAfterFleetSwitch('/app/deploys', '?fleet=zulu', 'zulu', list, true)).toBe('/app/deploys');
    expect(pathAfterFleetSwitch('/app/crew', '?fleet=zulu', 'yankee', list, true)).toBe('/app/crew');
  });

  it('replaces the history entry, so Back does not return to the mismatch', () => {
    sources.value = other;
    sourcesLoaded.value = true;
    provideFleets(() => fleets);
    page.pathname = '/app/deploys';
    page.search = '?fleet=zulu';
    const pushed: string[] = [];
    vi.stubGlobal('history', { ...history, pushState: (_s: unknown, _t: string, u: string) => pushed.push(u), replaceState: (_s: unknown, _t: string, url: string) => {
      const [pathname, search = ''] = url.split('?');
      page.pathname = pathname;
      page.search = search ? `?${search}` : '';
    } });
    startRouter();
    expect(page.pathname + page.search).toBe('/app/deploys?fleet=yankee');
    expect(pushed).toEqual([]);
  });
});

describe('a module that goes away after startup', () => {
  beforeEach(() => {
    page.install();
    page.search = '';
    page.pathname = '/app/inbox';
    sources.value = [mod('deploys')];
    sourcesLoaded.value = true;
  });
  afterEach(() => {
    sources.value = [];
    sourcesLoaded.value = false;
    loc.value = { path: '/app/inbox', search: '' };
  });

  it('sends the path to the inbox once the sources drop it', () => {
    const stop = effect(settleRoute);
    startRouter();
    go('/app/deploys', '');
    expect(page.pathname).toBe('/app/deploys');
    sources.value = [];
    expect(page.pathname).toBe('/app/inbox');
    stop();
  });

  it('keeps a deep link while its plugin starts, then opens it or falls back once it settles', () => {
    sources.value = [src('deploys', { state: 'starting', capabilities: [] })];
    page.pathname = '/app/deploys';
    const stop = effect(settleRoute);
    startRouter();
    expect(page.pathname).toBe('/app/deploys');
    sources.value = [mod('deploys')];
    expect(page.pathname).toBe('/app/deploys');
    sources.value = [src('deploys', { state: 'error', capabilities: [] })];
    expect(page.pathname).toBe('/app/inbox');
    stop();
  });
});
