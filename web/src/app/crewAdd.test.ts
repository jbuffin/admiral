import { afterEach, describe, expect, it, vi } from 'vitest';
import { addCrewStep, ADD_CREW_START, addShownFields, canDraft, createBody, createMember, CUSTOM_ROLE, DESCRIPTION_MAX, draftCrew, fieldError, readDraft, type AddCrewState, type NewMemberForm } from './crew';

const fetched: { url: string; init: RequestInit }[] = [];
const answer = (status: number, body: unknown) =>
  vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
    fetched.push({ url, init });
    return new Response(JSON.stringify(body), { status });
  });

afterEach(() => {
  vi.unstubAllGlobals();
  fetched.length = 0;
});

const draftReply = {
  draft: { name: 'quartermaster', role: 'implementer', 'based-on': '', skills: ['a', 'b'], model: '', harness: '', 'pick-me-when': 'Pick for stores.', instructions: 'Check the stores.' },
  problems: [],
};

describe('draftCrew', () => {
  it('posts the description to the fleet, with no harness query, and hands fetch the signal', async () => {
    answer(200, draftReply);
    const ctl = new AbortController();
    await draftCrew('zulu', 'A quartermaster.', ctl.signal);
    expect(fetched[0].url).toMatch(/\/fleets\/zulu\/crew\/draft$/);
    expect(fetched[0].init.method).toBe('POST');
    expect(JSON.parse(String(fetched[0].init.body))).toEqual({ description: 'A quartermaster.' });
    expect(fetched[0].init.signal).toBe(ctl.signal);
  });
});

describe('canDraft', () => {
  it('needs a description with something in it, of at most 4000 characters', () => {
    expect(DESCRIPTION_MAX).toBe(4000);
    expect(canDraft('')).toBe(false);
    expect(canDraft(' \n\t ')).toBe(false);
    expect(canDraft('a quartermaster')).toBe(true);
    expect(canDraft('é'.repeat(4000))).toBe(true);
    expect(canDraft('é'.repeat(4001))).toBe(false);
  });

  it('counts a character outside the basic plane once, as the server does', () => {
    expect(canDraft('😀'.repeat(4000))).toBe(true);
  });
});

describe('readDraft', () => {
  it('fills the editor form from a draft, a custom role into Custom… with its based-on', () => {
    const { form, problems } = readDraft({ draft: { ...draftReply.draft, role: 'purser', 'based-on': 'scribe' }, problems: [{ field: 'based-on', message: 'bad' }] });
    expect(form).toMatchObject({ name: 'quartermaster', role: CUSTOM_ROLE, customRole: 'purser', basedOn: 'scribe', skills: 'a\nb', pickMeWhen: 'Pick for stores.', instructions: 'Check the stores.' });
    expect(problems).toEqual([{ field: 'based-on', message: 'bad' }]);
  });

  it('reads a draft with nulls, a missing problems list and malformed problems without throwing', () => {
    const { form, problems } = readDraft({ draft: { name: null, role: 'implementer', skills: null, model: null, 'pick-me-when': null }, problems: [{ field: 4 }, null, { field: 'name', message: 'taken' }] });
    expect(form).toMatchObject({ name: '', skills: '', model: '', pickMeWhen: '', instructions: '' });
    expect(problems).toEqual([{ field: 'name', message: 'taken' }]);
    expect(readDraft({ draft: draftReply.draft }).problems).toEqual([]);
    expect(readDraft(null).form.name).toBe('');
  });
});

describe('createBody', () => {
  const form: NewMemberForm = { name: ' quartermaster ', role: 'implementer', customRole: '', basedOn: '', skills: 'a\n\n b ', model: '', harness: '', pickMeWhen: ' Pick. ', instructions: 'Work.\n', };

  it('adds the trimmed name to the member body and leaves out what is empty', () => {
    expect(createBody(form)).toEqual({ name: 'quartermaster', role: 'implementer', skills: ['a', 'b'], 'pick-me-when': 'Pick.', instructions: 'Work.' });
  });

  it('keeps a model and a harness the engineer set', () => {
    expect(createBody({ ...form, model: 'opus', harness: 'codex' })).toMatchObject({ model: 'opus', harness: 'codex' });
  });
});

describe('createMember', () => {
  it('posts the body to the fleet crew members route', async () => {
    answer(201, { name: 'quartermaster' });
    const r = await createMember('zulu', { name: 'quartermaster', role: 'implementer', 'pick-me-when': 'Pick.' });
    expect(r).toEqual({ ok: true, data: { name: 'quartermaster' } });
    expect(fetched[0].url).toMatch(/\/fleets\/zulu\/crew\/members$/);
    expect(fetched[0].init.method).toBe('POST');
    expect(JSON.parse(String(fetched[0].init.body))).toEqual({ name: 'quartermaster', role: 'implementer', 'pick-me-when': 'Pick.' });
  });
});

describe('the Name field', () => {
  it('shows above the edit form fields, and a 409 on name lands beside it', () => {
    expect(addShownFields('implementer')[0]).toBe('name');
    expect(addShownFields('implementer')).toContain('pick-me-when');
    expect(addShownFields('implementer')).not.toContain('based-on');
    expect(addShownFields(CUSTOM_ROLE)).toContain('based-on');
    expect(fieldError({ error: 'quartermaster is taken', field: 'name' }, addShownFields('implementer'))).toEqual({ field: 'name', message: 'quartermaster is taken' });
  });
});

describe('the Add crew panel', () => {
  const at = (s: AddCrewState, ...events: Parameters<typeof addCrewStep>[1][]) => events.reduce(addCrewStep, s);
  const typed = () => at(ADD_CREW_START, { type: 'edit', description: 'A quartermaster.' });

  it('starts at describe with an empty description', () => {
    expect(ADD_CREW_START).toMatchObject({ phase: 'describe', description: '' });
  });

  it('drafts a description that can be drafted, counting seconds, and refuses one that cannot', () => {
    expect(at(ADD_CREW_START, { type: 'draft' }).phase).toBe('describe');
    const drafting = at(typed(), { type: 'draft' }, { type: 'tick' }, { type: 'tick' });
    expect(drafting).toMatchObject({ phase: 'drafting', description: 'A quartermaster.', seconds: 2 });
  });

  it('opens the draft in the editor with its problems when the call answers', () => {
    const s = at(typed(), { type: 'draft' }, { type: 'result', result: { ok: true, data: { ...draftReply, problems: [{ field: 'name', message: 'taken' }] } } });
    expect(s.phase).toBe('draft');
    expect(s.form?.name).toBe('quartermaster');
    expect(s.problems).toEqual([{ field: 'name', message: 'taken' }]);
  });

  it('keeps the description on a failure and names its kind', () => {
    const s = at(typed(), { type: 'draft' }, { type: 'result', result: { ok: false, status: 502, error: 'timed out', kind: 'timeout' } });
    expect(s).toMatchObject({ phase: 'failed', description: 'A quartermaster.', kind: 'timeout', error: 'timed out' });
  });

  it('says drafting needs a proven harness, with the reason, on a 409', () => {
    const s = at(typed(), { type: 'draft' }, { type: 'result', result: { ok: false, status: 409, error: 'no harness', reason: 'claude is not proven' } });
    expect(s).toMatchObject({ phase: 'nojudge', reason: 'claude is not proven', description: 'A quartermaster.' });
  });

  it('goes back to the description when Stop aborts the call, and ignores the aborted answer', () => {
    const s = at(typed(), { type: 'draft' }, { type: 'stop' }, { type: 'result', result: { ok: false, aborted: true, error: 'aborted' } });
    expect(s).toMatchObject({ phase: 'describe', description: 'A quartermaster.' });
  });

  it('keeps the description when Discard leaves the draft', () => {
    const drafted = at(typed(), { type: 'draft' }, { type: 'result', result: { ok: true, data: draftReply } });
    expect(at(drafted, { type: 'discard' })).toMatchObject({ phase: 'describe', description: 'A quartermaster.' });
  });
});
