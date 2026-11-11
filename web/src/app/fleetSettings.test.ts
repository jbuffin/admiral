// Module under test: web/src/app/fleetSettings.ts
//   saveBody(shown: FleetFields, edited: FleetFields): Partial<FleetFields>
//     FleetFields = { search_roots: string[]; owners: string[]; clone_root: string; color: string }
//     `shown` is what the page drew from the view, `edited` what the form holds now. The result holds only the fields
//     that differ; a changed list is sent whole; a cleared clone_root or color is "" and a removed list is [].
//   errorTarget(message: string, fleetKey: string): { field: 'search_roots' | 'owners' | 'clone_root' | 'color'; index?: number } | null
//     Where a 400's `error` shows: the field its `fleets.<key>.<field>[i]` path names, or null for the page top.
import { describe, expect, it } from 'vitest';
import { errorTarget, saveBody } from './fleetSettings';

const shown = {
  search_roots: ['/Users/jo/code', '/Users/jo/work'],
  owners: ['acme'],
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

  it('names a single-value field', () => {
    expect(errorTarget('fleets.home.clone_root: must be an absolute path or start with ~/, got "x"', 'home')).toEqual({ field: 'clone_root' });
    expect(errorTarget('fleets.home.color: must be #RRGGBB, got "red"', 'home')).toEqual({ field: 'color' });
  });

  it('sends an error that names another fleet or no field to the page top', () => {
    expect(errorTarget('fleets.work.clone_root: must be an absolute path', 'home')).toBeNull();
    expect(errorTarget('malformed JSON body', 'home')).toBeNull();
  });
});
