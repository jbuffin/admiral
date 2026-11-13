import { useEffect, useRef, useState } from 'preact/hooks';
import { call, saveFleet, type Fleet } from './api';
import { capabilities, loadCapabilities } from './capabilities';
import { confirmFailure, confirmLabel, confirmOrdersBody, draftedCheckIndex, draftFailure, joinGuidance, keptLimits, proseError, remainingLoosens, routineProblem, type Kept, type OrdersDraft } from './filing';
import { fleets, loadFleets } from './fleets';
import { errorTarget, fieldMessage } from './fleetSettings';
import { go, limitsPath } from './router';
import { canAddCheck, checkTooLong, guidanceTooLong, loosenedSentences, MAX_CHECK_RUNES, MAX_GUIDANCE_RUNES, ordersErrorTarget, ordersMessage, runes, saveFailure, type Sentence, type StandingOrdersFields } from './standingOrders';
import type { RoutineForm } from './routines';
import { formOfRoutine, type Routine } from './routines';

const draftPath = (fleet: string, what: 'standing-orders' | 'routines') => `/fleets/${encodeURIComponent(fleet)}/${what}/draft`;
const ordersPath = (fleet: string) => `/fleets/${encodeURIComponent(fleet)}/standing-orders`;

interface OrdersAnswer {
  draft: OrdersDraft;
  sentences: Sentence[];
  changes: Sentence[];
  loosens: string[];
  problems: { field: string; message: string }[];
}

type Failure = { message: string; raw: string; status?: number; part: 'limits' | 'orders' };

/** The drafted value of one changed setting, in plain words. */
function settingChange(limits: OrdersDraft['limits'], field: string): string {
  let at: unknown = limits;
  for (const part of field.split('.')) at = at !== null && typeof at === 'object' ? (at as Record<string, unknown>)[part] : undefined;
  const text = typeof at === 'string' || typeof at === 'number' ? String(at) : JSON.stringify(at);
  return `${field.replace(/[._]/g, ' ')}: ${text}`;
}

const linkTo = (path: string) => (e: MouseEvent) => {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  go(path, '');
};

/** The input under the intro: Draft files prose through the drafter; with no judge it is a plain form that saves the text as a Check or Guidance. */
export function NewStandingOrder({ fleet, shown, unsaved, onFiled }: { fleet: Fleet; shown: StandingOrdersFields; unsaved: boolean; onFiled: (kept: Kept) => void }) {
  const [prose, setProse] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [offReason, setOffReason] = useState<string | null>(null);
  const [answer, setAnswer] = useState<OrdersAnswer | null>(null);
  const [removed, setRemoved] = useState<string[]>([]);
  const [checks, setChecks] = useState<string[]>([]);
  const [guidance, setGuidance] = useState('');
  const [armed, setArmed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<Failure | null>(null);
  const [kind, setKind] = useState<'check' | 'guidance' | 'limit'>('check');
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, [fleet.key]);

  const judge = capabilities.value.find((f) => f.key === fleet.key)?.judge;
  const plain = offReason !== null || (!!judge && !judge.harness);
  const reason = offReason ?? judge?.reason ?? '';

  const draft = async () => {
    const refused = proseError(prose);
    if (refused) return setError(refused);
    const key = fleet.key;
    setBusy(true);
    setError('');
    const r = await call<OrdersAnswer>('POST', draftPath(key, 'standing-orders'), { prose });
    // A late answer belongs to the fleet it was asked for.
    if (!live.current || key !== fleet.key) return;
    setBusy(false);
    if (r.ok) {
      setAnswer({ ...r.data, changes: r.data.changes ?? [], loosens: r.data.loosens ?? [], problems: r.data.problems ?? [], sentences: r.data.sentences ?? [] });
      setRemoved([]);
      setChecks(r.data.draft.checks ?? []);
      setGuidance(r.data.draft.guidance ?? '');
      setArmed(false);
      setFailure(null);
      return;
    }
    if (r.status === 409) {
      setOffReason(r.reason ?? r.error);
      void loadCapabilities();
      return;
    }
    setError(draftFailure(r));
  };

  const discard = () => {
    setAnswer(null);
    setFailure(null);
    setArmed(false);
  };

  const finish = (kept: Kept) => {
    setAnswer(null);
    setProse('');
    setFailure(null);
    setArmed(false);
    onFiled(kept);
  };

  const confirm = async () => {
    if (!answer) return;
    const loosening = remainingLoosens(answer.loosens, removed).length;
    if (loosening > 0 && !armed) return setArmed(true);
    const key = fleet.key;
    const limits = keptLimits(answer.draft.limits, removed);
    const kept = { checks, guidance: guidance.trim() === '' ? '' : guidance };
    const body = confirmOrdersBody(shown, kept);
    setSaving(true);
    setFailure(null);
    if (limits) {
      const r = await saveFleet(key, { limits });
      if (!live.current || key !== fleet.key) return;
      if (!r.ok) {
        setSaving(false);
        const raw = r.status === undefined ? saveFailure(r) : r.error;
        return setFailure({ message: confirmFailure('limits', false, raw), raw, status: r.status, part: 'limits' });
      }
      fleets.value = r.data;
    }
    if (Object.keys(body).length > 0) {
      const r = await call('PUT', ordersPath(key), body);
      if (!live.current || key !== fleet.key) return;
      if (!r.ok) {
        setSaving(false);
        // The Limits are stored; a retry must not send them again.
        if (limits) setAnswer({ ...answer, draft: { ...answer.draft, limits: null }, changes: [], loosens: [] });
        if (limits) onFiled({ checks: [], guidance: '' });
        const raw = r.status === undefined ? saveFailure(r) : r.error;
        return setFailure({ message: confirmFailure('orders', limits !== undefined, raw), raw, status: r.status, part: 'orders' });
      }
    }
    setSaving(false);
    finish(kept);
  };

  const plainSave = async () => {
    const text = prose.trim();
    if (!text) return setError('Write what you want first.');
    const key = fleet.key;
    const body = kind === 'check' ? confirmOrdersBody(shown, { checks: [text], guidance: '' }) : confirmOrdersBody(shown, { checks: [], guidance: text });
    setBusy(true);
    setError('');
    const r = await call('PUT', ordersPath(key), body);
    if (!live.current || key !== fleet.key) return;
    setBusy(false);
    if (!r.ok) return setError(ordersMessage(saveFailure(r)));
    setProse('');
    onFiled({ checks: [], guidance: '' });
  };

  const input = (
    <textarea
      rows={3}
      aria-label="New standing order"
      placeholder="Write a standing order, e.g. never touch the payments-api migrations folder"
      value={prose}
      disabled={busy || !!answer}
      onInput={(e) => {
        setProse((e.target as HTMLTextAreaElement).value);
        setError('');
      }}
    />
  );
  const proseRunes = runes(prose);

  if (plain) {
    const tooLong = kind === 'check' ? checkTooLong(prose.trim()) || !canAddCheck(shown.checks.length) : guidanceTooLong(joinGuidance(shown.guidance, prose.trim()));
    return (
      <form
        class="orders-new"
        aria-label="New standing order"
        onSubmit={(e) => {
          e.preventDefault();
          if (kind !== 'limit') void plainSave();
        }}
      >
        <p class="note">Drafting is off{reason ? `: ${reason}` : ''}. Save the text as a Check or Guidance, or use Check again below to turn drafting back on.</p>
        {input}
        <fieldset class="seg" role="radiogroup">
          <legend>Save it as</legend>
          {([['check', 'Check'], ['guidance', 'Guidance'], ['limit', "It's a Limit"]] as const).map(([k, label]) => (
            <label key={k} class={kind === k ? 'seg-opt on' : 'seg-opt'}>
              <input type="radio" name="orders-kind" checked={kind === k} onChange={() => setKind(k)} />
              {label}
            </label>
          ))}
        </fieldset>
        {kind === 'check' ? (
          <span class={tooLong ? 'note error' : 'note'}>
            {runes(prose.trim())}/{MAX_CHECK_RUNES}
            {!canAddCheck(shown.checks.length) ? ' The fleet already holds the most Checks.' : ''}
          </span>
        ) : null}
        {kind === 'guidance' ? (
          <span class={tooLong ? 'note error' : 'note'}>
            Added after the stored Guidance. {runes(joinGuidance(shown.guidance, prose.trim()))}/{MAX_GUIDANCE_RUNES}
          </span>
        ) : null}
        {error ? (
          <p class="note error" role="alert">
            {error}
          </p>
        ) : null}
        <div class="routine-actions">
          {kind === 'limit' ? (
            <a class="btn" href={limitsPath(fleet.key)} onClick={linkTo(limitsPath(fleet.key))}>
              Open Limits
            </a>
          ) : (
            <button type="submit" class="btn primary" disabled={busy || !prose.trim() || tooLong}>
              Save
            </button>
          )}
        </div>
      </form>
    );
  }

  const loosens = answer ? remainingLoosens(answer.loosens, removed) : [];
  const keptChecks = checks;
  const nothing = !answer || (keptLimits(answer.draft.limits, removed) === undefined && keptChecks.length === 0 && guidance.trim() === '');
  const problemsOf = (field: string) => (answer?.problems ?? []).filter((p) => p.field === field);
  const limitsFailure = failure?.part === 'limits' && failure.status !== 409 ? errorTarget(failure.raw, fleet.key) : null;
  const ordersTarget = failure?.part === 'orders' ? ordersErrorTarget(failure.raw) : null;
  const draftedIndex = failure?.part === 'orders' ? draftedCheckIndex(failure.raw, shown.checks.length) : null;
  const inline = (m: string) => <p class="note error" role="alert">{m}</p>;
  const hasGuidance = answer?.draft.guidance != null;

  return (
    <div class="orders-new" role="group" aria-label="Draft a standing order">
      {input}
      <span class={proseRunes > 4000 ? 'note error' : 'note'}>{proseRunes}/4000</span>
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      {!answer ? (
        <div class="routine-actions">
          <button type="button" class="btn primary" disabled={busy || !prose.trim()} aria-busy={busy} onClick={() => void draft()}>
            {busy ? 'Drafting…' : 'Draft'}
          </button>
        </div>
      ) : (
        <section class="set draft-review" aria-label="Draft review">
          <h3 class="section-title">Drafted from your text</h3>
          {answer.changes.length === 0 && checks.length === 0 && !guidance && answer.problems.length === 0 ? <p class="note">Nothing in that text changes a Limit, adds a Check or adds Guidance.</p> : null}

          {answer.changes.length > 0 ? (
            <div class="orders-group" role="group" aria-label="Drafted Limits">
              <h4 class="section-title">Limits</h4>
              <ul class="limit-list">
                {answer.changes.filter((c) => !removed.includes(c.field)).map((c) => (
                  <li key={c.field} class="row-field">
                    <span>{c.sentence}</span>
                    <span class="note">{settingChange(answer.draft.limits, c.field)}</span>
                    <button
                      type="button"
                      class="btn"
                      aria-label={`Remove change: ${c.sentence}`}
                      disabled={saving}
                      onClick={() => {
                        setRemoved([...removed, c.field]);
                        setArmed(false);
                      }}
                    >
                      Remove
                    </button>
                    {limitsFailure?.limit === c.field && failure ? inline(fieldMessage(failure.raw, fleet.key)) : null}
                  </li>
                ))}
              </ul>
              <p class="note">
                To change a value,{' '}
                <a href={limitsPath(fleet.key)} onClick={linkTo(limitsPath(fleet.key))}>
                  open the Limits page
                </a>
                .
              </p>
              {problemsOf('limits').map((p, i) => <p key={i} class="note error">{p.message}</p>)}
            </div>
          ) : problemsOf('limits').length > 0 ? (
            <div class="orders-group">
              <h4 class="section-title">Limits</h4>
              {problemsOf('limits').map((p, i) => <p key={i} class="note error">{p.message}</p>)}
            </div>
          ) : null}

          {checks.length > 0 || problemsOf('checks').length > 0 ? (
            <div class="orders-group" role="group" aria-label="Drafted Checks">
              <h4 class="section-title">Checks</h4>
              <ul class="limit-list">
                {checks.map((c, i) => (
                  <li key={i} class="row-field">
                    <input type="text" aria-label={`Drafted Check ${i + 1}`} value={c} disabled={saving} onInput={(e) => setChecks(checks.map((x, j) => (j === i ? (e.target as HTMLInputElement).value : x)))} />
                    <span class={checkTooLong(c) ? 'note error' : 'note'}>
                      {runes(c)}/{MAX_CHECK_RUNES}
                    </span>
                    <button type="button" class="btn" aria-label={`Remove drafted Check ${i + 1}`} disabled={saving} onClick={() => setChecks(checks.filter((_, j) => j !== i))}>
                      Remove
                    </button>
                    {draftedIndex === i && failure ? inline(ordersMessage(failure.raw)) : null}
                  </li>
                ))}
              </ul>
              {problemsOf('checks').map((p, i) => <p key={i} class="note error">{p.message}</p>)}
            </div>
          ) : null}

          {hasGuidance || problemsOf('guidance').length > 0 ? (
            <div class="orders-group" role="group" aria-label="Guidance to add">
              <h4 class="section-title">Guidance</h4>
              <p class="note">Added after the stored Guidance, with a blank line between.</p>
              {hasGuidance ? (
                <>
                  <textarea aria-label="Drafted Guidance" rows={4} value={guidance} disabled={saving} onInput={(e) => setGuidance((e.target as HTMLTextAreaElement).value)} />
                  <span class={guidanceTooLong(joinGuidance(shown.guidance, guidance)) ? 'note error' : 'note'}>
                    {runes(joinGuidance(shown.guidance, guidance))}/{MAX_GUIDANCE_RUNES}
                  </span>
                </>
              ) : null}
              {ordersTarget?.kind === 'guidance' && failure ? inline(ordersMessage(failure.raw)) : null}
              {problemsOf('guidance').map((p, i) => <p key={i} class="note error">{p.message}</p>)}
            </div>
          ) : null}

          {problemsOf('').length > 0 ? problemsOf('').map((p, i) => <p key={i} class="note error">{p.message}</p>) : null}

          {loosens.length > 0 ? (
            <div class="note warning" role="group" aria-label="Loosening">
              <p>These Limits would loosen:</p>
              <ul class="limit-list">
                {loosenedSentences(loosens, answer.sentences).map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
              <p>{armed ? 'Tap again to accept the loosening.' : 'Confirm needs a second tap.'}</p>
            </div>
          ) : null}
          {unsaved ? <p class="note">Your unsaved edits to the Checks and Guidance below stay unsaved and are kept; Confirm saves only the stored text and this draft.</p> : null}
          {failure ? (
            <p class="note error" role="alert">
              {failure.message}{' '}
              {failure.status === 409 ? (
                <button type="button" class="btn" onClick={() => void loadFleets()}>
                  Reload
                </button>
              ) : null}
            </p>
          ) : null}
          <div class="routine-actions">
            <button type="button" class="btn primary" disabled={saving || nothing || checks.some(checkTooLong) || guidanceTooLong(joinGuidance(shown.guidance, guidance))} onClick={() => void confirm()}>
              {confirmLabel(armed ? loosens.length : 0)}
            </button>
            <button type="button" class="btn" disabled={saving} onClick={discard}>
              Discard draft
            </button>
          </div>
        </section>
      )}
    </div>
  );
}

/** The input at the top of the Routines tab's list: Draft files prose as an unsaved routine in the editor. */
export function DescribeRoutine({ fleet, onDrafted }: { fleet: Fleet; onDrafted: (form: RoutineForm, problem?: { field: 'name' | 'when' | 'before_acting' | 'do'; message: string }) => void }) {
  const [prose, setProse] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, [fleet.key]);

  const draft = async () => {
    const refused = proseError(prose);
    if (refused) return setError(refused);
    const key = fleet.key;
    setBusy(true);
    setError('');
    const r = await call<{ draft: Omit<Routine, 'id'>; problems: { field: string; message: string }[] | null }>('POST', draftPath(key, 'routines'), { prose });
    if (!live.current || key !== fleet.key) return;
    setBusy(false);
    if (!r.ok) {
      if (r.status === 409) void loadCapabilities();
      return setError(draftFailure(r));
    }
    const first = r.data.problems?.[0];
    onDrafted(formOfRoutine({ ...r.data.draft, id: '' }), first ? routineProblem(first) : undefined);
    setProse('');
  };

  return (
    <div class="orders-new">
      <input
        type="text"
        aria-label="Describe a routine"
        placeholder="Describe a routine, e.g. every Friday, list stale PRs"
        value={prose}
        disabled={busy}
        onInput={(e) => {
          setProse((e.target as HTMLInputElement).value);
          setError('');
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !busy) void draft();
        }}
      />
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      <button type="button" class="btn" disabled={busy || !prose.trim()} aria-busy={busy} onClick={() => void draft()}>
        {busy ? 'Drafting…' : 'Draft'}
      </button>
    </div>
  );
}
