import { describe, expect, it } from 'vitest';
import { errorTarget, fieldMessage, saveBody } from './fleetSettings';

const shown = {
  search_roots: ['/Users/jo/code', '/Users/jo/work'],
  owners: ['acme'],
  wake_reviewers: ['alice'],
  clone_root: '/Users/jo/clones',
  color: '#1a2b3c',
};

describe('saveBody', () => {
  it('sends nothing when nothing changed', () => {
    expect(saveBody(shown, { ...shown })).toEqual({});
  });

  it('sends only the colour when only the colour changed, so the stored form of the others stays', () => {
    const body = saveBody(shown, { ...shown, color: '#445566' });
    expect(body).toEqual({ color: '#445566' });
    for (const k of ['search_roots', 'owners', 'clone_root']) expect(body).not.toHaveProperty(k);
  });

  it('sends a changed list whole, with the entries it did not touch as shown', () => {
    const body = saveBody(shown, { ...shown, search_roots: ['/Users/jo/code', '/Users/jo/elsewhere'] });
    expect(body).toEqual({ search_roots: ['/Users/jo/code', '/Users/jo/elsewhere'] });
  });

  it('sends a list with an added owner whole', () => {
    expect(saveBody(shown, { ...shown, owners: ['acme', 'extra'] })).toEqual({ owners: ['acme', 'extra'] });
  });

  it('sends a list with an added wake reviewer whole', () => {
    expect(saveBody(shown, { ...shown, wake_reviewers: ['alice', 'bob'] })).toEqual({ wake_reviewers: ['alice', 'bob'] });
  });

  it('sends an empty wake reviewers list when the last one was removed, and nothing when they are unchanged', () => {
    expect(saveBody(shown, { ...shown, wake_reviewers: [] })).toEqual({ wake_reviewers: [] });
    expect(saveBody(shown, { ...shown, color: '#445566' })).not.toHaveProperty('wake_reviewers');
  });

  it('sends an empty list when every owner or search root was removed', () => {
    expect(saveBody(shown, { ...shown, owners: [] })).toEqual({ owners: [] });
    expect(saveBody(shown, { ...shown, search_roots: [] })).toEqual({ search_roots: [] });
  });

  it('sends an empty clone root when the field was cleared', () => {
    expect(saveBody(shown, { ...shown, clone_root: '' })).toEqual({ clone_root: '' });
  });

  it('sends an empty colour for "Use default"', () => {
    expect(saveBody(shown, { ...shown, color: '' })).toEqual({ color: '' });
  });
});

describe('errorTarget', () => {
  it('names the list entry a search root error points at', () => {
    expect(errorTarget('fleets.home.search_roots[1]: must be an absolute path or start with ~/, got "x"', 'home')).toEqual({ field: 'search_roots', index: 1 });
    expect(errorTarget('fleets.home.owners[0]: must not be empty', 'home')).toEqual({ field: 'owners', index: 0 });
  });

  it('names the wake reviewer entry an error points at', () => {
    expect(errorTarget('fleets.home.wake_reviewers[2]: must be a GitHub login, got "a b"', 'home')).toEqual({ field: 'wake_reviewers', index: 2 });
    expect(fieldMessage('fleets.home.wake_reviewers[2]: must be a GitHub login, got "a b"', 'home')).toBe('must be a GitHub login, got "a b"');
  });

  it('names a single-value field', () => {
    expect(errorTarget('fleets.home.clone_root: must be an absolute path or start with ~/, got "x"', 'home')).toEqual({ field: 'clone_root' });
    expect(errorTarget('fleets.home.color: must be #RRGGBB, got "red"', 'home')).toEqual({ field: 'color' });
  });

  it('maps a Limits path to the sentence field it sits under, by longest prefix', () => {
    const at = (path: string) => errorTarget(`fleets.home.limits.${path}: not valid`, 'home');
    expect(at('quiet_hours[0].start')).toEqual({ field: 'limits', limit: 'quiet_hours' });
    expect(at('ceilings.t4.threshold.level')).toEqual({ field: 'limits', limit: 'ceilings.t4' });
    expect(at('ceilings.t1')).toEqual({ field: 'limits', limit: 'ceilings.t1' });
    expect(at('skip.labels[0].value')).toEqual({ field: 'limits', limit: 'skip.labels' });
    expect(at('repo_allowlist[3]')).toEqual({ field: 'limits', limit: 'repo_allowlist' });
    expect(at('rates.concurrent')).toEqual({ field: 'limits', limit: 'rates.concurrent' });
    expect(at('wall_clock_minutes')).toEqual({ field: 'limits', limit: 'wall_clock_minutes' });
  });

  it('sends an error that names another fleet or no field to the page top', () => {
    expect(errorTarget('fleets.work.clone_root: must be an absolute path', 'home')).toBeNull();
    expect(errorTarget('malformed JSON body', 'home')).toBeNull();
  });
});

describe('fieldMessage', () => {
  it('drops the path the field already shows', () => {
    expect(fieldMessage('fleets.home.clone_root: must be an absolute path or start with ~/, got "x"', 'home')).toBe('must be an absolute path or start with ~/, got "x"');
    expect(fieldMessage('fleets.home.owners[0]: must not be empty', 'home')).toBe('must not be empty');
  });

  it('keeps a message that names no field of this fleet whole', () => {
    expect(fieldMessage('fleets.work.clone_root: must be absolute', 'home')).toBe('fleets.work.clone_root: must be absolute');
    expect(fieldMessage('malformed JSON body', 'home')).toBe('malformed JSON body');
  });
});
