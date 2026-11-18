import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, expect, test, vi } from 'vitest';
import type { Entry } from './inbox';
import { lockedBy, REMOVED_FOLD, submitFold } from './itemActions';
import { type Acting, blankReview, EventProposalBody, Failure, FindingsBody, RemoveRow, ReviewBody, RoutineProposalBody, useActing, useFindings } from './PeekItem';

afterEach(() => vi.unstubAllGlobals());

const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';

const entry = (id: string, item: Record<string, unknown>) =>
  ({
    key: `item:s1/${id}`,
    type: 'item',
    session: `s-${id}`,
    id,
    folded: null,
    since: 0,
    item: { id, ...item },
  }) as unknown as Entry;

interface Sent {
  path: string;
  body: unknown;
}

function serve(reply: (path: string) => Response | Promise<Response>) {
  const sent: Sent[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      sent.push({
        path,
        body: init?.body ? JSON.parse(init.body as string) : undefined,
      });
      return reply(path);
    }),
  );
  return sent;
}

const refusal = (status: number, error: string) => new Response(JSON.stringify({ error }), { status });
const posts = (sent: Sent[]) => sent.filter((s) => !s.path.endsWith('/event'));
const press = (name: string | RegExp, within_: HTMLElement | Document = document) => fireEvent.click(within(within_ as HTMLElement).getByRole('button', { name }));

// Peek wires the review the same way: a step held above the body, and the verbs run through useActing.
function Review({ e, fold }: { e: Entry; fold: (t: string) => void }) {
  const acting = useActing(e, fold, lockedBy);
  const [ui, setUi] = useState(blankReview);
  return (
    <>
      <ReviewBody
        entry={e}
        ui={ui}
        set={(p) => setUi((r) => ({ ...r, ...p }))}
        acting={acting}
        submit={() => acting.run('submit', { event: ui.event, edited: ui.edited }, (d) => submitFold(d, ui.event))}
        discard={() => acting.run('discard', {}, () => 'Review discarded')}
      />
      <Failure message={acting.error} />
    </>
  );
}

const review = (id: string) =>
  entry(id, {
    kind: 'review',
    title: 'Review the visitor diff',
    review: {
      pr_url: 'https://github.com/acme/widgets/pull/12',
      review_id: 123,
      summary: 'Two nits and a bug',
    },
  });

test('a review shows its PR and review id, and Submit goes through a confirm before one request names the event', async () => {
  const sent = serve(() => new Response('{}'));
  const fold = vi.fn();
  render(<Review e={review('rv-submit')} fold={fold} />);
  expect(screen.getByRole('link', { name: /acme\/widgets#12/ })).toBeTruthy();
  expect(screen.getByText(/Pending review #123/)).toBeTruthy();
  expect(screen.getByText('Two nits and a bug')).toBeTruthy();

  press('Submit');
  press(/COMMENT/, screen.getByRole('group', { name: 'Submit the review as' }));
  expect(screen.getByText(/posted to the PR as you/)).toBeTruthy();
  expect(sent).toEqual([]);

  press('Submit COMMENT');
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Review submitted: COMMENT'));
  expect(sent).toEqual([
    {
      path: '/api/v1/inbox/s-rv-submit/rv-submit/submit',
      body: { event: 'COMMENT', edited: false },
    },
  ]);
});

test('Discard says the draft comments are deleted from GitHub, and only the confirm sends the request', async () => {
  const sent = serve(() => new Response('{}'));
  const fold = vi.fn();
  render(<Review e={review('rv-discard')} fold={fold} />);
  press('Discard');
  expect(screen.getByText(/deleted from GitHub/)).toBeTruthy();
  expect(sent).toEqual([]);

  press('Keep it');
  expect(screen.getByRole('button', { name: 'Submit' })).toBeTruthy();
  press('Discard');
  press('Discard review');
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Review discarded'));
  expect(sent).toEqual([{ path: '/api/v1/inbox/s-rv-discard/rv-discard/discard', body: {} }]);
});

test('a review GitHub refuses shows the error and keeps the actions, a busy 409 can be retried, and one already answered locks them', async () => {
  let answer = refusal(502, 'github said no');
  serve(() => answer);
  render(<Review e={review('rv-refused')} fold={vi.fn()} />);
  press('Submit');
  press(/APPROVE/, screen.getByRole('group', { name: 'Submit the review as' }));
  const submit = () => screen.getByRole('button', { name: 'Submit APPROVE' }) as HTMLButtonElement;

  press('Submit APPROVE');
  expect((await screen.findByRole('alert')).textContent).toBe('github said no');
  expect(submit().disabled).toBe(false);
  await waitFor(() => expect(submit().getAttribute('aria-busy')).not.toBe('true'));
  await waitFor(() => expect((screen.getByRole('button', { name: 'Change' }) as HTMLButtonElement).disabled).toBe(false));

  answer = refusal(409, 'another action on this item is still running');
  press('Submit APPROVE');
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('still running'));
  expect(submit().disabled).toBe(false);
  await waitFor(() => expect(submit().getAttribute('aria-busy')).not.toBe('true'));

  answer = refusal(409, 'inbox item is already answered');
  press('Submit APPROVE');
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('already answered'));
  expect((screen.getByRole('button', { name: 'Submit' }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole('button', { name: 'Discard' }) as HTMLButtonElement).disabled).toBe(true);
});

const findings = entry('f-cards', {
  kind: 'findings',
  findings: {
    pr_url: 'https://github.com/acme/widgets/pull/12',
    cards: ['c1', 'c2', 'c3'].map((id) => ({
      id,
      source: `reviewer on ${id}`,
      quote: id === 'c1' ? HOSTILE : `Quote for ${id}`,
      verdict: 'agree',
      reason: `Reason for ${id}`,
      proposal: { kind: 'reply', content: `Proposed reply for ${id}` },
      evidence: [],
    })),
  },
});

function Findings({ e }: { e: Entry }) {
  const acting = useActing(e, vi.fn(), lockedBy);
  const state = useFindings(e);
  return <FindingsBody entry={e} acting={acting} state={state} />;
}

test('findings: every card is decided, an open edit blocks Send, and Send posts the decisions once', async () => {
  const sent = serve(() => new Response('{}'));
  const { container } = render(<Findings e={findings} />);
  expect(screen.getByText(HOSTILE)).toBeTruthy();
  expect(container.querySelector('img, script')).toBeNull();
  const card = (n: number) => screen.getByRole('region', { name: `Finding card ${n}` });
  const send = screen.getByRole('button', {
    name: 'Send',
  }) as HTMLButtonElement;
  expect(screen.getByText('0 of 3 decided')).toBeTruthy();
  expect(send.disabled).toBe(true);

  press('Approve', card(1));
  press('Reject', card(3));
  press('Edit', card(2));
  expect(screen.getByText('Save or Cancel the open edit first.')).toBeTruthy();
  expect(send.disabled).toBe(true);
  fireEvent.input(screen.getByRole('textbox', { name: 'Edit the proposal for card 2' }), { target: { value: 'My own reply' } });
  press('Save');
  expect(screen.getByText('3 of 3 decided')).toBeTruthy();
  expect(send.disabled).toBe(false);

  fireEvent.click(send);
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0]).toEqual({
    path: '/api/v1/inbox/s-f-cards/f-cards/findings',
    body: {
      cards: [
        { id: 'c1', decision: 'approved' },
        {
          id: 'c2',
          decision: 'edited',
          proposal: { kind: 'reply', content: 'My own reply' },
        },
        { id: 'c3', decision: 'rejected' },
      ],
    },
  });
});

function Remove({ e, fold }: { e: Entry; fold?: (data: never) => string }) {
  const acting = useActing(e, vi.fn(), lockedBy);
  const [confirm, setConfirm] = useState(false);
  return <RemoveRow entry={e} confirm={confirm} setConfirm={setConfirm} acting={acting as Acting} fold={fold} />;
}

test('Remove from inbox asks first, Keep it backs out, and the confirm sends one remove', async () => {
  const sent = serve(() => new Response('{}'));
  render(<Remove e={entry('q-port', { kind: 'question' })} />);
  press('Remove from inbox');
  expect(screen.getByText(/The ship never gets an answer from here/)).toBeTruthy();
  expect(sent).toEqual([]);

  press('Keep it');
  press('Remove from inbox');
  press('Remove');
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0].path).toBe('/api/v1/inbox/s-q-port/q-port/remove');
  expect(REMOVED_FOLD).toBe('Removed from the inbox');
});

test.each([
  ['review', 'Nothing changes on GitHub', 'Keep it'],
  ['mission', 'counts as Drop: nothing is saved', 'Cancel'],
  ['crew_notes', 'counts as Drop: its notes are discarded', 'Cancel'],
])('Remove on a %s item says what it means and offers the matching way back', (kind, copy, back) => {
  render(<Remove e={entry(`rm-${kind}`, { kind })} />);
  press('Remove from inbox');
  expect(screen.getByText(new RegExp(copy, 'i'))).toBeTruthy();
  expect(screen.getByRole('button', { name: back })).toBeTruthy();
});

test('Remove on an allow rule proposal says nothing is added or skipped and offers Cancel', () => {
  render(<Remove e={entry('rm-allow', { kind: 'allow_rules' })} />);
  press('Remove from inbox');
  expect(screen.getByText('Remove this proposal? Nothing is added or skipped, so its rules can be proposed again.')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
});

const proposal = entry('routine-t1', {
  kind: 'routine_proposal',
  routine_proposal: {
    fleet: 'zulu',
    routine: 'r1',
    routine_name: 'Held sweep',
    action: 'launch claude in the proj checkout: go',
    reason: 'Hold fire is on',
    expires_at: new Date(Date.now() + 3_600_000).toISOString(),
  },
});

function Proposal({ e, fold, event }: { e: Entry; fold: (t: string) => void; event?: boolean }) {
  const acting = useActing(e, fold, lockedBy);
  return event ? <EventProposalBody entry={e} acting={acting} /> : <RoutineProposalBody entry={e} acting={acting} />;
}

test('Launch on a held routine sends one launch while it runs, with Dismiss off meanwhile, then folds', async () => {
  let release: () => void = () => undefined;
  const held = new Promise<void>((r) => (release = r));
  const sent = serve(async () => {
    await held;
    return new Response('{}');
  });
  const fold = vi.fn();
  render(<Proposal e={proposal} fold={fold} />);
  expect(screen.getByText('Hold fire is on')).toBeTruthy();
  const launch = screen.getByRole('button', { name: 'Launch' });
  fireEvent.click(launch);
  await waitFor(() => expect(launch.getAttribute('aria-busy')).toBe('true'));
  expect((screen.getByRole('button', { name: 'Dismiss' }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(launch);
  expect(sent).toHaveLength(1);

  release();
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Launched'));
  expect(sent).toEqual([{ path: '/api/v1/inbox/s-routine-t1/routine-t1/launch', body: {} }]);
});

test('Dismiss on a held routine sends one dismiss and launches nothing', async () => {
  const sent = serve(() => new Response('{}'));
  const fold = vi.fn();
  render(<Proposal e={entry('routine-t2', proposal.item as unknown as Record<string, unknown>)} fold={fold} />);
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Dismissed'));
  expect(sent.map((s) => s.path)).toEqual(['/api/v1/inbox/s-routine-t2/routine-t2/dismiss']);
});

const event = entry('event-trig-ask', {
  kind: 'event_proposal',
  event_proposal: {
    fleet: 'zulu',
    routine: 'r-ask',
    routine_name: 'Ask watch',
    trigger: 'trig-ask',
    tier: 'T3',
    expires_at: new Date(Date.now() + 3_600_000).toISOString(),
  },
});
const detail = {
  summary: `Mallory asks for a release edit. ${HOSTILE}`,
  title: 'Release edit',
  sender: 'mallory-the-stranger',
  link: 'https://github.com/acme/api/pull/8',
  metadata: false,
  mute_by: ['sender'],
  verdict: 'ask',
  check_id: '0bbdf84d',
  check: 'Never launch on release workflow edits',
  reviewer_reason: 'touches the release path',
};

test('an event proposal shows the untrusted summary as text, why it was asked, and Mute rule by sender', async () => {
  const sent = serve((path) => (path.endsWith('/event') ? new Response(JSON.stringify(detail)) : new Response('{}')));
  const { container } = render(<Proposal e={event} fold={vi.fn()} event />);
  expect(screen.getByText('Untrusted')).toBeTruthy();
  expect((await screen.findByText(/Mallory asks for a release edit\./)).textContent).toContain(HOSTILE);
  expect(container.querySelector('img, script')).toBeNull();
  expect(screen.getByText('From mallory-the-stranger')).toBeTruthy();
  expect(screen.getByText('Tier T3')).toBeTruthy();
  expect(screen.getByText('Asked about Check 0bbdf84d')).toBeTruthy();
  expect(screen.getByText('Check: Never launch on release workflow edits')).toBeTruthy();
  expect(screen.getByText('Reviewer: touches the release path')).toBeTruthy();

  press('Mute rule');
  press(/Mute this sender/, screen.getByRole('group', { name: 'Mute rule' }));
  expect(await screen.findByText('Muted this sender')).toBeTruthy();
  expect(sent.filter((s) => s.path.endsWith('/mute'))).toEqual([
    {
      path: '/api/v1/fleets/zulu/routines/r-ask/mute',
      body: { by: 'sender', value: 'mallory-the-stranger' },
    },
  ]);
  expect(posts(sent)).toHaveLength(1);
});

test('an event whose text is gone says so and still offers Launch and Dismiss', async () => {
  serve((path) => (path.endsWith('/event') ? refusal(404, 'gone') : new Response('{}')));
  render(<Proposal e={entry('event-gone', event.item as unknown as Record<string, unknown>)} fold={vi.fn()} event />);
  expect(await screen.findByText("The event's text is no longer available.")).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Launch' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Dismiss' })).toBeTruthy();
});

const MD = '## Heading\n\n- first point\n\n[the docs](https://example.com/docs) and `inline_code`\n\n```\nfenced line\n```';
const mdIn = (root: Element) => {
  expect(root.querySelector('h1, h2, h3, h4')?.textContent).toBe('Heading');
  expect(root.querySelector('li')?.textContent).toBe('first point');
  expect(root.querySelector('a[href="https://example.com/docs"]')?.textContent).toBe('the docs');
  expect([...root.querySelectorAll('code')].some((c) => c.textContent === 'inline_code')).toBe(true);
  expect(root.querySelector('pre > code')?.textContent).toContain('fenced line');
};

test('a review summary renders as markdown, and a script in it stays text', () => {
  const e = entry('md-review', { kind: 'review', review: { pr_url: 'https://github.com/acme/widgets/pull/12', review_id: 1, summary: MD } });
  const { container } = render(<Review e={e} fold={vi.fn()} />);
  mdIn(container);
  cleanup();
  const hostile = render(<Review e={entry('md-review-x', { kind: 'review', review: { review_id: 1, summary: '<script>window.__xss=1</script>' } })} fold={vi.fn()} />);
  expect(hostile.container.querySelector('script')).toBeNull();
  expect(hostile.container.textContent).toContain('<script>window.__xss=1</script>');
});

test('a finding card renders its quote, its reason and a reply proposal as markdown, with the reply kept raw in Edit', () => {
  const e = entry('md-cards', {
    kind: 'findings',
    findings: { pr_url: 'https://github.com/acme/widgets/pull/12', cards: [{ id: 'c1', source: 'reviewer', quote: MD, verdict: 'agree', reason: MD, proposal: { kind: 'reply', content: MD }, evidence: [] }] },
  });
  render(<Findings e={e} />);
  const card = screen.getByRole('region', { name: 'Finding card 1' });
  expect(card.querySelectorAll('h1, h2, h3, h4')).toHaveLength(3);
  expect(card.querySelectorAll('a[href="https://example.com/docs"]')).toHaveLength(3);
  expect(card.querySelectorAll('pre > code')).toHaveLength(3);
  expect(card.querySelector('blockquote')?.querySelector('h1, h2, h3, h4')?.textContent).toBe('Heading');
  expect(card.querySelector('.inbox-proposal')?.querySelector('li')?.textContent).toBe('first point');
  expect(within(card).getByText('Why:')).toBeTruthy();

  press('Edit', card);
  expect((screen.getByRole('textbox', { name: 'Edit the proposal for card 1' }) as HTMLTextAreaElement).value).toBe(MD);
});

test('a finding card with a script in its quote shows it as text', () => {
  const e = entry('md-cards-x', { kind: 'findings', findings: { cards: [{ id: 'c1', source: 's', quote: '<script>window.__xss=1</script>', verdict: 'agree', reason: 'r', proposal: null, evidence: [] }] } });
  const { container } = render(<Findings e={e} />);
  expect(container.querySelector('script')).toBeNull();
  expect(container.textContent).toContain('<script>window.__xss=1</script>');
});

test('an event summary and a routine proposal stay plain text', async () => {
  serve((path) => (path.endsWith('/event') ? new Response(JSON.stringify({ ...detail, summary: 'Alice wants **bold** and `code`' })) : new Response('{}')));
  const { container } = render(<Proposal e={event} fold={vi.fn()} event />);
  expect(await screen.findByText(/Alice wants \*\*bold\*\* and `code`/)).toBeTruthy();
  expect([...container.querySelectorAll('strong, code')].map((n) => n.textContent)).not.toContain('bold');
  expect(container.querySelector('code')).toBeNull();
  cleanup();
  const routine = entry('routine-md', { kind: 'routine_proposal', routine_proposal: { fleet: 'zulu', routine: 'r1', action: 'run **this**', reason: 'because `x`', expires_at: '' } });
  const r = render(<Proposal e={routine} fold={vi.fn()} />);
  expect(r.container.textContent).toContain('run **this**');
  expect([...r.container.querySelectorAll('strong, code')].map((n) => n.textContent)).not.toContain('this');
  expect(r.container.querySelector('code')).toBeNull();
});
