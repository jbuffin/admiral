import { describe, expect, it } from 'vitest';
import { createNoteDecisions, crewMemberFold, crewRemoveFold, noteEditProblems, readCrewMember, readCrewNotes } from './crewItems';

const note = (id: string, extra: Record<string, unknown> = {}) => ({ id, member: 'scout', role: 'scribe', scope: 'fleet', note: `Fact ${id}`, replaces: null, ...extra });

describe('reading a crew item a ship wrote', () => {
  it('degrades anything that is not an object to an empty item, never a throw', () => {
    for (const raw of [undefined, null, 'x', 42, [], true]) {
      expect(readCrewNotes(raw)).toEqual({ fleet: '', ship: '', run_date: '', notes: [] });
      expect(readCrewMember(raw)).toEqual({ fleet: '', ship: '', run_date: '', member: '', role: '', ad_hoc_run: '', definition: '', notes: [] });
    }
  });

  it('reads wrong-typed fields as empty and drops notes it cannot address', () => {
    const item = readCrewNotes({ fleet: 7, ship: ['a'], run_date: null, notes: [note('n1', { note: 5, member: {}, role: null }), 'junk', null, { member: 'no id' }, note('n1'), note('n2')] });
    expect(item.fleet).toBe('');
    expect(item.ship).toBe('');
    expect(item.run_date).toBe('');
    expect(item.notes.map((n) => n.id)).toEqual(['n1', 'n2']);
    expect(item.notes[0]).toMatchObject({ note: '', member: '', role: '' });
    expect(readCrewNotes({ notes: 'not a list' }).notes).toEqual([]);
  });

  it('keeps item order, a string replaces, and gives a fleet note no repo key', () => {
    const item = readCrewNotes({
      fleet: 'zulu',
      ship: 'Dory',
      run_date: '2026-11-12',
      notes: [note('n1', { replaces: 'old line' }), note('n2', { scope: 'repo', repo: 'acme/api' }), note('n3', { replaces: 4 })],
    });
    expect(item).toMatchObject({ fleet: 'zulu', ship: 'Dory', run_date: '2026-11-12' });
    expect(item.notes.map((n) => n.id)).toEqual(['n1', 'n2', 'n3']);
    expect(item.notes[0].replaces).toBe('old line');
    expect(item.notes[2].replaces).toBeNull();
    expect('repo' in item.notes[0]).toBe(false);
    expect(item.notes[1]).toMatchObject({ scope: 'repo', repo: 'acme/api' });
  });

  it('reads a member item with its definition and notes, and degrades its missing fields', () => {
    const m = readCrewMember({ fleet: 'zulu', ship: 'Dory', run_date: '2026-11-12', member: 'deckhand', role: 'implementer', ad_hoc_run: 'run-c', definition: '---\nname: deckhand\n---\n', notes: [note('n1', { member: 'deckhand' })] });
    expect(m).toMatchObject({ member: 'deckhand', role: 'implementer', ad_hoc_run: 'run-c', definition: '---\nname: deckhand\n---\n' });
    expect(m.notes).toHaveLength(1);
    expect(readCrewMember({ member: 'deckhand', definition: 9, notes: null })).toMatchObject({ member: 'deckhand', definition: '', notes: [] });
  });
});

describe('the crew notes decisions and their payload', () => {
  const notes = () => readCrewNotes({ notes: [note('n1', { replaces: 'old line' }), note('n2'), note('n3', { scope: 'repo', repo: 'acme/api' })] }).notes;

  it('gates Send on every note having a decision', () => {
    const d = createNoteDecisions(notes());
    expect(d.progress()).toEqual({ decided: 0, total: 3 });
    expect(d.canSend()).toBe(false);
    d.keep('n1');
    d.drop('n2');
    expect(d.canSend()).toBe(false);
    d.save('n3', { note: 'Own words', scope: 'fleet' });
    expect(d.progress()).toEqual({ decided: 3, total: 3 });
    expect(d.canSend()).toBe(true);
  });

  it('sends a keep and a drop as the decision alone, never a replaces', () => {
    const d = createNoteDecisions(notes());
    d.keep('n1');
    d.drop('n2');
    d.keep('n3');
    expect(d.payload()).toEqual({ notes: { n1: { decision: 'keep' }, n2: { decision: 'drop' }, n3: { decision: 'keep' } } });
    expect(JSON.stringify(d.payload())).not.toContain('replaces');
  });

  it('sends an edit with note and scope, repo only for repo scope, and leaves replaces out', () => {
    const d = createNoteDecisions(notes());
    d.save('n1', { note: 'Edited fleet fact', scope: 'fleet', repo: 'acme/stale' });
    d.save('n2', { note: 'Edited repo fact', scope: 'repo', repo: 'acme/web' });
    d.drop('n3');
    expect(d.payload()).toEqual({
      notes: {
        n1: { decision: 'edit', note: 'Edited fleet fact', scope: 'fleet' },
        n2: { decision: 'edit', note: 'Edited repo fact', scope: 'repo', repo: 'acme/web' },
        n3: { decision: 'drop' },
      },
    });
    expect(JSON.stringify(d.payload())).not.toContain('replaces');
  });

  it('lets a later decision replace an earlier one, and ignores an id the item does not have', () => {
    const d = createNoteDecisions(notes());
    d.save('n2', { note: 'Edited', scope: 'fleet' });
    d.drop('n2');
    d.keep('nope');
    expect(d.decision('n2')).toBe('drop');
    expect(d.progress().decided).toBe(1);
  });
});

describe('what an edited note must satisfy before Save', () => {
  it('accepts a note of 1 to 300 characters on one line, for fleet scope', () => {
    expect(noteEditProblems({ note: 'x', scope: 'fleet' })).toEqual({});
    expect(noteEditProblems({ note: 'x'.repeat(300), scope: 'fleet' })).toEqual({});
  });

  it('refuses an empty note, one over 300 characters, and one with a line break', () => {
    expect(noteEditProblems({ note: '', scope: 'fleet' }).note).toBeTruthy();
    expect(noteEditProblems({ note: '   ', scope: 'fleet' }).note).toBeTruthy();
    expect(noteEditProblems({ note: 'x'.repeat(301), scope: 'fleet' }).note).toBeTruthy();
    expect(noteEditProblems({ note: 'one\ntwo', scope: 'fleet' }).note).toBeTruthy();
  });

  it('wants <owner>/<repo> for repo scope and ignores the repo for fleet scope', () => {
    expect(noteEditProblems({ note: 'x', scope: 'repo', repo: 'acme/api' })).toEqual({});
    for (const repo of ['', 'acme', 'acme/api/x', '/api', 'acme/', 'ac me/api']) {
      expect(noteEditProblems({ note: 'x', scope: 'repo', repo }).repo, repo).toBeTruthy();
    }
    expect(noteEditProblems({ note: 'x', scope: 'fleet', repo: 'nonsense' })).toEqual({});
  });
});

describe('the fold line after a crew member is answered', () => {
  it('says a kept member joined the fleet crew', () => {
    expect(crewMemberFold('keep', 'scout', { file_removed: true })).toBe("Kept scout in the fleet's crew");
  });

  it('says a drop that deleted the file, and one that left it in place', () => {
    const gone = crewMemberFold('drop', 'scout', { file_removed: true });
    const left = crewMemberFold('drop', 'scout', { file_removed: false });
    expect(gone).toContain('Dropped scout');
    expect(gone).not.toContain('left in place');
    expect(left).toBe('Dropped scout; the ad hoc file was left in place');
  });

  it('says the same for Remove, which counts as a drop', () => {
    expect(crewRemoveFold({ file_removed: false }, 'scout')).toContain('left in place');
    expect(crewRemoveFold({ file_removed: true }, 'scout')).not.toContain('left in place');
    expect(crewRemoveFold({}, 'scout')).toContain('left in place');
  });
});
