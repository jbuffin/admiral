import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads.
const page = vi.hoisted(() => {
  const l = { pathname: '/app/inbox', search: '' };
  vi.stubGlobal('location', l);
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', {
    replaceState: (_s: unknown, _t: string, url: string) => {
      const [pathname, search = ''] = url.split('?');
      l.pathname = pathname;
      l.search = search ? `?${search}` : '';
    },
  });
  return l;
});

import { installPath, usageAllPath, usagePath, campaignOf, campaignPath, crewOf, crewPath, loc, preferencesFleet, preferencesPage, startRouter, surfaceOf, peekOf, peekPath, chatSettingsPath, chatTerminalPath, chatTerminalOf, chatPeekOf, automateOf, automateSearch, routineNewPath, routineOf, routinePath } from './router';

describe('campaign paths', () => {
  it('round-trips an id through its page path', () => {
    expect(campaignPath('3f9c0a41b7d2e8a5')).toBe('/app/campaigns/3f9c0a41b7d2e8a5');
    expect(campaignOf('/app/campaigns/3f9c0a41b7d2e8a5')).toBe('3f9c0a41b7d2e8a5');
    expect(campaignOf('/app/campaigns/3f9c0a41b7d2e8a5/')).toBe('3f9c0a41b7d2e8a5');
  });
  it('names no campaign on the list page or another surface', () => {
    expect(campaignOf('/app/campaigns')).toBeNull();
    expect(campaignOf('/app/ships')).toBeNull();
  });
  it('is the Campaigns surface, and no surface below it deeper than the id', () => {
    expect(surfaceOf('/app/campaigns/3f9c0a41b7d2e8a5')?.id).toBe('campaigns');
    expect(surfaceOf('/app/campaigns/3f9c0a41b7d2e8a5/extra')).toBeUndefined();
  });
});

describe('crew paths', () => {
  it('round-trips a member name through its page path, escaping what needs it', () => {
    expect(crewPath('ada')).toBe('/app/crew/ada');
    expect(crewOf('/app/crew/ada')).toBe('ada');
    expect(crewOf('/app/crew/ada/')).toBe('ada');
    expect(crewOf(crewPath('a b/c'))).toBe('a b/c');
  });
  it('names no member on the list page or another surface', () => {
    expect(crewOf('/app/crew')).toBeNull();
    expect(crewOf('/app/ships')).toBeNull();
    expect(crewOf('/app/crew/a/b')).toBeNull();
  });
  it('is the Crew surface, and no surface below it deeper than the name', () => {
    expect(surfaceOf('/app/crew/ada')?.id).toBe('crew');
    expect(surfaceOf('/app/crew/ada/extra')).toBeUndefined();
  });
});

describe('routine paths', () => {
  it('round-trips an id through its page path, escaping what needs it', () => {
    expect(routinePath('9f2c')).toBe('/app/routines/9f2c');
    expect(routineOf('/app/routines/9f2c')).toBe('9f2c');
    expect(routineOf('/app/routines/9f2c/')).toBe('9f2c');
    expect(routineOf(routinePath('a b/c'))).toBe('a b/c');
    // and names none on the list, the new-routine path or another surface
    expect(routineOf('/app/routines')).toBeNull();
    expect(routineOf(routineNewPath)).toBeNull();
    expect(routineOf('/app/crew/ada')).toBeNull();
  });
  it('is the Routines surface, the new-routine path too, and nothing deeper than the id', () => {
    expect(surfaceOf('/app/routines/9f2c')?.id).toBe('routines');
    expect(surfaceOf(routineNewPath)?.id).toBe('routines');
    expect(surfaceOf('/app/routines/9f2c/extra')).toBeUndefined();
  });
  it('round-trips the view an Automate… query names, with its fleet', () => {
    expect(automateOf(automateSearch('gh', 'mine', 'work'))).toEqual({ source: 'gh', view: 'mine' });
    expect(automateOf(automateSearch('a&b', 'x y=z'))).toEqual({ source: 'a&b', view: 'x y=z' });
    expect(automateSearch('gh', 'mine', 'work')).toContain('fleet=work');
    expect(automateOf('?fleet=work')).toBeNull();
  });
});

describe('the router', () => {
  beforeEach(() => {
    page.search = '?fleet=zulu';
  });
  it('keeps a Campaign page path on load', () => {
    page.pathname = '/app/campaigns/3f9c0a41b7d2e8a5';
    startRouter();
    expect(page.pathname).toBe('/app/campaigns/3f9c0a41b7d2e8a5');
    expect(loc.value.path).toBe('/app/campaigns/3f9c0a41b7d2e8a5');
  });
  it('still sends an unknown path to the inbox', () => {
    page.pathname = '/app/nowhere';
    startRouter();
    expect(page.pathname).toBe('/app/inbox');
  });
});

describe('the ship route', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.stubGlobal('location', { pathname: '/app/ships', search: '' });
  });
  afterEach(() => {
    vi.stubGlobal('location', page);
  });

  it('is a ship under Ships, and a path with more below it is still unknown', async () => {
    const { surfaceOf, shipSession } = await import('./router');
    expect(surfaceOf('/app/ships/s-abc')?.id).toBe('ships');
    expect(shipSession('/app/ships/s-abc')).toBe('s-abc');
    expect(shipSession('/app/ships')).toBeNull();
    expect(shipSession('/app/inbox')).toBeNull();
    expect(surfaceOf('/app/ships/s-abc/more')).toBeUndefined();
  });

  it('carries a session whose name needs escaping', async () => {
    const { shipPath, shipSession } = await import('./router');
    const path = shipPath('a b/c');
    expect(path).toBe('/app/ships/a%20b%2Fc');
    expect(shipSession(path)).toBe('a b/c');
  });

  it('survives a reload and keeps ?fleet=', async () => {
    vi.stubGlobal('location', { pathname: '/app/ships/s-abc', search: '?fleet=alpha' });
    const replaceState = vi.fn();
    vi.stubGlobal('history', { replaceState, pushState: vi.fn() });
    vi.stubGlobal('addEventListener', vi.fn());
    const { startRouter, loc } = await import('./router');
    startRouter();
    expect(replaceState).not.toHaveBeenCalled();
    expect(loc.value).toEqual({ path: '/app/ships/s-abc', search: '?fleet=alpha' });
  });
});

describe('Remote access route', () => {
  it('is its own Preferences surface under the reserved admiral segment, and a fleet named remote or admiral keeps its pages', () => {
    expect(surfaceOf('/app/preferences/admiral/remote')?.id).toBe('preferences');
    expect(preferencesFleet('/app/preferences/admiral/remote')).toBeNull();
    expect(preferencesPage('/app/preferences/admiral/remote')).toBe('remote');

    expect(preferencesFleet('/app/preferences/remote/fleet')).toBe('remote');
    expect(preferencesPage('/app/preferences/remote/fleet')).toBe('fleet');
    expect(preferencesFleet('/app/preferences/admiral/sources')).toBe('admiral');
    expect(preferencesPage('/app/preferences/admiral/sources')).toBe('sources');
    expect(surfaceOf('/app/preferences/admiral/bogus')).toBeUndefined();
    expect(surfaceOf('/app/preferences/zulu/remote')).toBeUndefined();
  });
});

describe('Install app route', () => {
  it('lives under the reserved admiral segment, and install under any other key is no page', () => {
    expect(installPath).toBe('/app/preferences/admiral/install');
    expect(surfaceOf(installPath)?.id).toBe('preferences');
    expect(preferencesFleet(installPath)).toBeNull();
    expect(preferencesPage(installPath)).toBe('install');
    expect(surfaceOf('/app/preferences/zulu/install')).toBeUndefined();
  });
});

describe('Usage routes', () => {
  it('has a page under each fleet and a cross-fleet page under the reserved admiral segment', () => {
    expect(usagePath('zulu')).toBe('/app/preferences/zulu/usage');
    expect(usageAllPath).toBe('/app/preferences/admiral/all-usage');
    expect(surfaceOf('/app/preferences/zulu/usage')?.id).toBe('preferences');
    expect(preferencesFleet('/app/preferences/zulu/usage')).toBe('zulu');
    expect(preferencesPage('/app/preferences/zulu/usage')).toBe('usage');
    expect(surfaceOf(usageAllPath)?.id).toBe('preferences');
    expect(preferencesFleet(usageAllPath)).toBeNull();
    expect(preferencesPage(usageAllPath)).toBe('all-usage');
  });

  it('leaves admiral/usage to a fleet keyed admiral', () => {
    expect(usagePath('admiral')).not.toBe(usageAllPath);
    expect(surfaceOf(usagePath('admiral'))?.id).toBe('preferences');
    expect(preferencesFleet(usagePath('admiral'))).toBe('admiral');
    expect(preferencesPage(usagePath('admiral'))).toBe('usage');
    expect(surfaceOf('/app/preferences/zulu/all-usage')).toBeUndefined();
  });
});

describe('the Peek route', () => {
  it('names a ship stop, a crew boat stop and a file item, each a path under the Inbox', () => {
    expect(peekPath({ session: 's-abc' })).toBe('/app/inbox/s-abc');
    expect(peekPath({ session: 's-abc', crew: 'scout' })).toBe('/app/inbox/s-abc/crew/scout');
    expect(peekPath({ session: 's-abc', item: 'q-port' })).toBe('/app/inbox/s-abc/item/q-port');
    for (const p of ['/app/inbox/s-abc', '/app/inbox/s-abc/crew/scout', '/app/inbox/s-abc/item/q-port']) expect(surfaceOf(p)?.id).toBe('inbox');
    expect(peekOf('/app/inbox/s-abc')).toEqual({ session: 's-abc' });
    expect(peekOf('/app/inbox/s-abc/crew/scout')).toEqual({ session: 's-abc', crew: 'scout' });
    expect(peekOf('/app/inbox/s-abc/item/q-port')).toEqual({ session: 's-abc', item: 'q-port' });
    expect(peekPath({ session: 's-abc', merge: true })).toBe('/app/inbox/s-abc/merge');
    expect(peekOf('/app/inbox/s-abc/merge')).toEqual({ session: 's-abc', merge: true });
    expect(peekOf('/app/inbox/s-abc/item/q-port/')).toEqual({ session: 's-abc', item: 'q-port' });
  });

  it('round-trips segments that need escaping', () => {
    const target = { session: 'a b', item: 'q 1/2' };
    expect(peekPath(target)).toBe('/app/inbox/a%20b/item/q%201%2F2');
    expect(peekOf(peekPath(target))).toEqual(target);
  });

  it('names no Peek on the list, another surface or a path it does not know', () => {
    expect(peekOf('/app/inbox')).toBeNull();
    expect(peekOf('/app/ships/s-abc')).toBeNull();
    expect(peekOf('/app/inbox/s-abc/%E0%A4%A')).toBeNull();
    for (const p of ['/app/inbox/s-abc/crew', '/app/inbox/s-abc/item', '/app/inbox/s-abc/other/x', '/app/inbox/s-abc/item/q/extra', '/app/inbox/s-abc/crew/scout/extra']) {
      expect(peekOf(p), p).toBeNull();
      expect(surfaceOf(p), p).toBeUndefined();
    }
  });

  it('names no Peek for a session or crew name holding a slash, which would share a queue key with another path', () => {
    expect(peekOf('/app/inbox/a%2Fb')).toBeNull();
    expect(peekOf('/app/inbox/a%2Fb/crew/c')).toBeNull();
    expect(peekOf('/app/inbox/a/crew/b%2Fc')).toBeNull();
  });

  it('survives a reload on a Peek path, and an unknown path under the Inbox lands on the list', async () => {
    vi.resetModules();
    const open = async (pathname: string) => {
      vi.stubGlobal('location', { pathname, search: '?fleet=alpha' });
      const replaceState = vi.fn();
      vi.stubGlobal('history', { replaceState, pushState: vi.fn() });
      vi.stubGlobal('addEventListener', vi.fn());
      const { startRouter } = await import('./router');
      startRouter();
      return replaceState;
    };
    expect(await open('/app/inbox/s-abc/item/q-port')).not.toHaveBeenCalled();
    vi.resetModules();
    expect(await open('/app/inbox/s-abc/nowhere/x')).toHaveBeenCalledWith(null, '', '/app/inbox?fleet=alpha');
    vi.stubGlobal('location', page);
  });
});

describe("a fleet's AdmiralBot page", () => {
  it('has its own path below the fleet and names that page', () => {
    expect(chatSettingsPath('harbor')).toBe('/app/preferences/harbor/admiralbot');
    expect(preferencesFleet('/app/preferences/harbor/admiralbot')).toBe('harbor');
    expect(preferencesPage('/app/preferences/harbor/admiralbot')).toBe('admiralbot');
  });
  it('is part of Preferences', () => {
    expect(surfaceOf('/app/preferences/harbor/admiralbot')?.id).toBe('preferences');
  });
});

describe('chat terminal paths', () => {
  it('round-trips a fleet key through its terminal path, which belongs to the Chat surface', () => {
    expect(chatTerminalPath('harbor')).toBe('/app/chat/harbor/terminal');
    expect(chatTerminalOf('/app/chat/harbor/terminal')).toBe('harbor');
    expect(surfaceOf('/app/chat/harbor/terminal')?.id).toBe('chat');
    expect(chatPeekOf('/app/chat/harbor/terminal')).toBeNull();
  });

  it('encodes a fleet key so a slash or a space survives, and a key named terminal or item stays itself', () => {
    for (const key of ['a/b', 'my fleet', 'terminal', 'item', '100%']) {
      expect(chatTerminalOf(chatTerminalPath(key))).toBe(key);
    }
  });

  it('names no terminal for other paths', () => {
    for (const path of ['/app/chat', '/app/chat/harbor', '/app/chat/harbor/terminal/x', '/app/chat//terminal', '/app/inbox/harbor/terminal', '/app/chat/%E0%A4%A/terminal']) {
      expect(chatTerminalOf(path)).toBeNull();
    }
    expect(surfaceOf('/app/chat/harbor/terminal/x')).toBeUndefined();
  });
});

describe('Notifications route', () => {
  it('is a global page under the reserved admiral segment, and a fleet named admiral has no such page', () => {
    expect(surfaceOf('/app/preferences/admiral/notifications')?.id).toBe('preferences');
    expect(preferencesFleet('/app/preferences/admiral/notifications')).toBeNull();
    expect(preferencesPage('/app/preferences/admiral/notifications')).toBe('notifications');
    expect(surfaceOf('/app/preferences/zulu/notifications')).toBeUndefined();
  });
});
