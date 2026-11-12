import { afterEach, describe, expect, it, vi } from 'vitest';
import { call } from './api';

const answer = (status: number, body: unknown) => vi.stubGlobal('fetch', async () => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status }));

afterEach(() => vi.unstubAllGlobals());

describe('call on a refusal', () => {
  it('carries the note id a 409 names, with its error', async () => {
    answer(409, { error: 'scope full', note: 'n2' });
    expect(await call('POST', '/inbox/_admiral/c-1/crew-notes', {})).toEqual({ ok: false, status: 409, error: 'scope full', note: 'n2' });
  });

  it('leaves note off a refusal that names none, and keeps field for the callers that read it', async () => {
    answer(409, { error: 'fleet is no longer configured' });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 409, error: 'fleet is no longer configured' });
    answer(400, { error: 'bad role', field: 'role' });
    expect(await call('PUT', '/x')).toEqual({ ok: false, status: 400, error: 'bad role', field: 'role' });
  });

  it('ignores a note that is not a string', async () => {
    answer(409, { error: 'nope', note: 4 });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 409, error: 'nope' });
  });
});
