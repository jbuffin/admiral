import { describe, expect, it } from 'vitest';
import { confirmFailure, confirmLabel, confirmOrdersBody, draftFailure, draftedCheckIndex, joinGuidance, keptLimits, proseError, remainingLoosens, routineProblem } from './filing';

describe('keptLimits', () => {
  const patch = { wall_clock_minutes: 1440, rates: { concurrent: 4, launches_per_hour: 3 }, ceilings: { t1: { level: 'A3' } } };

  it('keeps the whole patch when nothing is removed', () => {
    expect(keptLimits(patch, [])).toEqual(patch);
  });

  it('drops a removed top-level change', () => {
    expect(keptLimits(patch, ['wall_clock_minutes'])).toEqual({ rates: patch.rates, ceilings: patch.ceilings });
  });

  it('drops one member of a group and keeps the rest', () => {
    expect(keptLimits(patch, ['rates.concurrent'])).toEqual({ wall_clock_minutes: 1440, rates: { launches_per_hour: 3 }, ceilings: patch.ceilings });
  });

  it('drops a group key once its last member is removed', () => {
    expect(keptLimits(patch, ['ceilings.t1'])).not.toHaveProperty('ceilings');
  });

  it('sends no limits when every change is removed', () => {
    expect(keptLimits(patch, ['wall_clock_minutes', 'rates.concurrent', 'rates.launches_per_hour', 'ceilings.t1'])).toBeUndefined();
  });

  it('sends no limits when the draft has none', () => {
    expect(keptLimits(null, [])).toBeUndefined();
  });

  it('does not change the patch it was given', () => {
    keptLimits(patch, ['rates.concurrent']);
    expect(patch.rates).toEqual({ concurrent: 4, launches_per_hour: 3 });
  });
});

describe('joinGuidance', () => {
  it('puts a blank line between the stored text and the drafted text', () => {
    expect(joinGuidance('Prefer small PRs.', 'Ask before CI changes.')).toBe('Prefer small PRs.\n\nAsk before CI changes.');
  });

  it('sends the drafted text alone, with no leading blank line, when none is stored', () => {
    expect(joinGuidance('', 'Ask before CI changes.')).toBe('Ask before CI changes.');
  });
});

describe('confirmOrdersBody', () => {
  const shown = { checks: ['Run make check.'], guidance: 'Prefer small PRs.' };

  it('appends the kept Checks to the stored ones and joins the kept Guidance', () => {
    expect(confirmOrdersBody(shown, { checks: ['Ask first.'], guidance: 'Be brief.' })).toEqual({ checks: ['Run make check.', 'Ask first.'], guidance: 'Prefer small PRs.\n\nBe brief.' });
  });

  it('sends only the Checks when no Guidance is kept', () => {
    expect(confirmOrdersBody(shown, { checks: ['Ask first.'], guidance: '' })).toEqual({ checks: ['Run make check.', 'Ask first.'] });
  });

  it('sends only the Guidance when no Check is kept', () => {
    expect(confirmOrdersBody(shown, { checks: [], guidance: 'Be brief.' })).toEqual({ guidance: 'Prefer small PRs.\n\nBe brief.' });
  });

  it('sends the kept Guidance alone when none is stored', () => {
    expect(confirmOrdersBody({ checks: [], guidance: '' }, { checks: [], guidance: 'Be brief.' })).toEqual({ guidance: 'Be brief.' });
  });

  it('sends nothing when nothing is kept', () => {
    expect(confirmOrdersBody(shown, { checks: [], guidance: '' })).toEqual({});
  });
});

describe('remainingLoosens', () => {
  it('lists every loosened Limit when nothing is removed', () => {
    expect(remainingLoosens(['wall_clock_minutes', 'rates.concurrent'], [])).toEqual(['wall_clock_minutes', 'rates.concurrent']);
  });

  it('clears the warning of a removed change and keeps the others in order', () => {
    expect(remainingLoosens(['ceilings.t3', 'wall_clock_minutes', 'rates.concurrent'], ['wall_clock_minutes'])).toEqual(['ceilings.t3', 'rates.concurrent']);
  });

  it('leaves none when every loosening change is removed', () => {
    expect(remainingLoosens(['wall_clock_minutes'], ['wall_clock_minutes', 'rates.concurrent'])).toEqual([]);
  });
});

describe('confirmLabel', () => {
  it('is plain Confirm when nothing loosens', () => {
    expect(confirmLabel(0)).toBe('Confirm');
  });

  it('names how many Limits the second tap loosens', () => {
    expect(confirmLabel(1)).toBe('Confirm, loosening 1 Limits');
    expect(confirmLabel(3)).toBe('Confirm, loosening 3 Limits');
  });
});

describe('draftedCheckIndex', () => {
  it('maps a Check past the stored ones to its drafted row', () => {
    expect(draftedCheckIndex('invalid standing orders: checks[3]: is longer than 300 characters', 2)).toBe(1);
    expect(draftedCheckIndex('invalid standing orders: checks[2]: is empty', 2)).toBe(0);
  });

  it('is null for a stored Check', () => {
    expect(draftedCheckIndex('invalid standing orders: checks[1]: is empty', 2)).toBeNull();
  });

  it('counts from zero when none is stored', () => {
    expect(draftedCheckIndex('invalid standing orders: checks[0]: is empty', 0)).toBe(0);
  });

  it('is null for a message about something else', () => {
    expect(draftedCheckIndex('invalid standing orders: guidance: is too long', 1)).toBeNull();
    expect(draftedCheckIndex('boom', 1)).toBeNull();
  });
});

describe('proseError', () => {
  it('refuses blank and whitespace-only prose', () => {
    expect(proseError('')).toBe('prose: write what you want');
    expect(proseError('  \n\t ')).toBe('prose: write what you want');
  });

  it('allows 4000 characters and refuses 4001', () => {
    expect(proseError('x'.repeat(4000))).toBeNull();
    expect(proseError('x'.repeat(4001))).toBe('prose: at most 4000 characters');
  });

  it('counts characters, not UTF-16 code units', () => {
    expect(proseError('😀'.repeat(4000))).toBeNull();
    expect(proseError('😀'.repeat(4001))).toBe('prose: at most 4000 characters');
  });

  it('allows ordinary prose', () => {
    expect(proseError('never touch the payments-api migrations folder')).toBeNull();
  });
});

describe('draftFailure', () => {
  it('gives the server sentence, then says nothing was saved', () => {
    expect(draftFailure({ error: 'The drafting call did not answer in time.', status: 502 })).toBe('The drafting call did not answer in time. Nothing was saved.');
    expect(draftFailure({ error: 'The drafting call ran out of budget.', status: 502 })).toBe('The drafting call ran out of budget. Nothing was saved.');
  });

  it('says plainly when the request never reached Admiral', () => {
    expect(draftFailure({ error: 'Failed to fetch' })).toBe("Couldn't reach Admiral. Nothing was saved.");
  });

  it('shows a 400 as the server worded it', () => {
    expect(draftFailure({ error: 'prose: write what you want', status: 400 })).toBe('prose: write what you want');
  });
});

describe('confirmFailure', () => {
  it('says nothing was saved when the Limits save fails', () => {
    expect(confirmFailure('limits', false, 'the limits changed elsewhere')).toBe('Nothing was saved: the limits changed elsewhere');
  });

  it('says the Limits were saved and the Checks and Guidance were not when the orders save fails after it', () => {
    expect(confirmFailure('orders', true, 'boom')).toBe("The Limits were saved, but the Checks and Guidance weren't: boom");
  });

  it('says nothing was saved when the orders save fails and no Limits went first', () => {
    expect(confirmFailure('orders', false, 'boom')).toBe('Nothing was saved: boom');
  });
});

describe('routineProblem', () => {
  it('prefixes the field word the server stripped, with the editor label', () => {
    expect(routineProblem({ field: 'name', message: 'is required' })).toEqual({ field: 'name', message: 'Name is required' });
    expect(routineProblem({ field: 'when', message: 'the cron expression "0 0 30 2 *" never fires' })).toEqual({ field: 'when', message: 'When the cron expression "0 0 30 2 *" never fires' });
    expect(routineProblem({ field: 'before_acting', message: 'must be report_only, ask_first or act' })).toEqual({ field: 'before_acting', message: 'Before acting must be report_only, ask_first or act' });
    expect(routineProblem({ field: 'do', message: 'the prompt is empty' })).toEqual({ field: 'do', message: 'Do the prompt is empty' });
  });
});
