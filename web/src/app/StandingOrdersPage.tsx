import { useEffect, useState } from 'preact/hooks';
import { call, type Fleet } from './api';
import { capabilities, loadCapabilities } from './capabilities';
import { fleets, loadFleets } from './fleets';
import { harnessName } from './harnessName';
import { JudgeLine } from './HarnessCapabilities';
import { MissingCapability } from './MissingCapability';
import { RoutineTabs } from './RoutineTabs';
import { go, limitsPath } from './router';
import { canAddCheck, checkTooLong, guidanceTooLong, judgeGap, judgeOff, loosenedSentences, MAX_CHECKS, MAX_CHECK_RUNES, MAX_GUIDANCE_RUNES, NEEDS_JUDGE, ordersErrorTarget, ordersMessage, ordersPatch, runes, saveFailure, type Sentence } from './standingOrders';

interface Orders {
  checks: string[];
  guidance: string;
  preset: string;
  sentences: Sentence[];
  overridden: string[];
}

const base = (key: string) => `/fleets/${encodeURIComponent(key)}/standing-orders`;
const PRESET: Record<string, string> = { work: 'Work', home: 'Home' };
function NeedsJudge({ fleet }: { fleet: Fleet }) {
  const current = capabilities.value.find((f) => f.key === fleet.key);
  return (
    <section class="orders-group needs-judge" aria-label="Needs the judge">
      <p class="line">These stay off until a harness is proven safe for judge calls.</p>
      <ul class="set-list off-features">
        {NEEDS_JUDGE.map((f) => (
          <li key={f} class="muted">
            {f}
          </li>
        ))}
      </ul>
      {fleet.docks.map((d) => {
        const gap = judgeGap(current?.harnesses.find((h) => h.harness === d.harness));
        const name = harnessName(d.harness);
        if (gap?.status === 'failed') return <MissingCapability key={d.harness} fleet={fleet.key} harness={d.harness} capability={gap.capability} />;
        const reason = gap ? `${gap.capability} has not been checked on ${name}.` : `Judge calls are not proven on ${name}.`;
        return <MissingCapability key={d.harness} fleet={fleet.key} harness={d.harness} reason={reason} />;
      })}
    </section>
  );
}

function CopyTo({ fleet, onCopied }: { fleet: Fleet; onCopied: () => void }) {
  const others = fleets.value.filter((f) => f.key !== fleet.key);
  const [open, setOpen] = useState(false);
  const [to, setTo] = useState(others[0]?.key ?? '');
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; status?: number } | null>(null);
  const [done, setDone] = useState<{ name: string; loosened: string[] } | null>(null);
  if (others.length === 0) return null;
  const target = others.find((f) => f.key === to) ?? others[0]!;
  const copy = async () => {
    setBusy(true);
    setError(null);
    const r = await call<Orders & { loosens?: string[] }>('POST', `${base(fleet.key)}/copy`, { to: target.key });
    setBusy(false);
    setConfirm(false);
    if (!r.ok) return setError({ message: saveFailure(r), status: r.status });
    setOpen(false);
    setDone({ name: target.name, loosened: loosenedSentences(r.data.loosens ?? [], r.data.sentences) });
    void loadFleets();
    onCopied();
  };
  return (
    <div class="orders-copy">
      {!open && !confirm ? (
        <button type="button" class="btn" onClick={() => { setOpen(true); setDone(null); }}>
          Copy to another fleet
        </button>
      ) : null}
      {open && !confirm ? (
        <div class="row-field">
          <label class="field">
            <span>Copy to</span>
            <select aria-label="Copy to" value={target.key} onChange={(e) => setTo((e.target as HTMLSelectElement).value)}>
              {others.map((f) => (
                <option key={f.key} value={f.key}>
                  {f.name}
                </option>
              ))}
            </select>
          </label>
          <button type="button" class="btn" onClick={() => setConfirm(true)}>
            Copy
          </button>
          <button type="button" class="btn" onClick={() => setOpen(false)}>
            Cancel
          </button>
        </div>
      ) : null}
      {confirm ? (
        <div class="set confirm" role="group" aria-label={`Copy to ${target.name}`}>
          <p class="note">Copying replaces {target.name}’s Checks, Guidance, preset and Limits with this fleet’s.</p>
          <div class="routine-actions">
            <button type="button" class="btn danger" disabled={busy} onClick={() => void copy()}>
              Replace {target.name}’s
            </button>
            <button type="button" class="btn" disabled={busy} onClick={() => setConfirm(false)}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}
      {error ? (
        <p class="note error" role="alert">
          {error.status === 409 ? 'The config file changed or is invalid, so nothing was copied. ' : ''}
          {error.message}{' '}
          {error.status === 409 ? (
            <button type="button" class="btn" onClick={() => { setError(null); void loadFleets(); onCopied(); }}>
              Reload
            </button>
          ) : null}
        </p>
      ) : null}
      {done ? (
        <div role="status">
          <p class="note">Copied to {done.name}.</p>
          {done.loosened.length > 0 ? (
            <>
              <p class="note">These Limits are now looser there:</p>
              <ul class="limit-list">
                {done.loosened.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
            </>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** The Routines screen's Standing orders tab: the active fleet's Limits as sentences, its Checks and its Guidance. */
export function StandingOrdersPage({ fleet, title }: { fleet: Fleet; title: boolean }) {
  const [shown, setShown] = useState<Orders | null>(null);
  const [checks, setChecks] = useState<string[]>([]);
  const [guidance, setGuidance] = useState('');
  const [newCheck, setNewCheck] = useState('');
  const [failed, setFailed] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const adopt = (o: Orders, keep?: Partial<{ checks: boolean; guidance: boolean }>) => {
    setShown(o);
    if (!keep?.checks) setChecks(o.checks);
    if (!keep?.guidance) setGuidance(o.guidance);
  };
  const load = async () => {
    const r = await call<Orders>('GET', base(fleet.key));
    if (r.ok) {
      adopt({ ...r.data, checks: r.data.checks ?? [], sentences: r.data.sentences ?? [], overridden: r.data.overridden ?? [] });
      setFailed('');
    } else setFailed(r.error);
  };
  useEffect(() => {
    void load();
    void loadCapabilities();
  }, [fleet.key]);

  const judge = capabilities.value.find((f) => f.key === fleet.key)?.judge;
  const save = async (only: 'checks' | 'guidance', nextChecks: string[], nextGuidance: string) => {
    if (!shown) return;
    const body = ordersPatch({ checks: shown.checks, guidance: shown.guidance }, { checks: nextChecks, guidance: nextGuidance }, only);
    if (Object.keys(body).length === 0) return;
    setBusy(true);
    setError('');
    const r = await call<Orders>('PUT', base(fleet.key), body);
    setBusy(false);
    if (r.ok) {
      // A key the person did not send keeps what they typed.
      adopt({ ...r.data, checks: r.data.checks ?? [], sentences: r.data.sentences ?? [], overridden: r.data.overridden ?? [] }, { checks: !('checks' in body), guidance: !('guidance' in body) });
      if ('checks' in body) setNewCheck('');
    } else setError(saveFailure(r));
  };

  const target = error ? ordersErrorTarget(error, checks.length) : null;
  const message = error ? ordersMessage(error) : '';
  const checksDirty = shown ? ordersPatch({ checks: shown.checks, guidance: shown.guidance }, { checks, guidance: shown.guidance }, 'checks').checks !== undefined : false;
  const guidanceDirty = shown ? guidance !== shown.guidance : false;
  const adding = newCheck.trim();
  const sentences = shown?.sentences ?? [];
  const link = (field: string) => limitsPath(fleet.key, field);

  return (
    <main class="panel crew routines standing-orders">
      <div class="routines-head">
        {title ? <h1>Routines</h1> : null}
        <span class="pill">{fleet.name} fleet only</span>
      </div>
      <RoutineTabs current="standing-orders" />
      <p class="line">Standing orders apply only to work started by outside events: a Limit Admiral enforces, a Check a reviewer judges, or Guidance added to a ship’s prompt.</p>
      {judge ? <JudgeLine judge={judge} /> : null}
      {judgeOff(capabilities.value.find((f) => f.key === fleet.key)) ? <NeedsJudge fleet={fleet} /> : null}
      {failed ? (
        <p class="alert" role="alert">
          {failed}
        </p>
      ) : null}
      {error && !target ? (
        <p class="alert" role="alert">
          {message}
        </p>
      ) : null}
      {!shown && !failed ? <p class="line">Loading…</p> : null}
      {shown ? (
        <>
          <section class="orders-group" aria-labelledby="orders-limits">
            <h2 id="orders-limits" class="section-title">
              Limits
            </h2>
            <p class="note">
              Admiral enforces these. {PRESET[shown.preset] ?? shown.preset} preset.
            </p>
            <ul class="limit-list">
              {sentences.map((s, i) => (
                <li key={`${s.field}-${i}`}>
                  {s.field ? (
                    <a
                      href={link(s.field)}
                      onClick={(e) => {
                        if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
                        e.preventDefault();
                        go(link(s.field), '');
                      }}
                    >
                      {s.sentence}
                    </a>
                  ) : (
                    s.sentence
                  )}
                  {s.field && shown.overridden.includes(s.field) ? <span class="changed"> Changed from the preset</span> : null}
                </li>
              ))}
            </ul>
          </section>

          <section class="orders-group" aria-labelledby="orders-checks">
            <h2 id="orders-checks" class="section-title">
              Checks
            </h2>
            <p class="note">A reviewer agent checks each action. {checks.length} of {MAX_CHECKS}.</p>
            {target?.kind === 'checks' ? (
              <p class="note error" role="alert">
                {message}
              </p>
            ) : null}
            <ul class="limit-list">
              {checks.map((c, i) => (
                <li key={i} class="row-field">
                  <input type="text" aria-label={`Check ${i + 1}`} value={c} disabled={busy} onInput={(e) => setChecks(checks.map((x, j) => (j === i ? (e.target as HTMLInputElement).value : x)))} />
                  <span class={checkTooLong(c) ? 'note error' : 'note'}>
                    {runes(c)}/{MAX_CHECK_RUNES}
                  </span>
                  <button type="button" class="btn" disabled={busy} aria-label={`Remove Check ${i + 1}`} onClick={() => void save('checks', checks.filter((_, j) => j !== i), guidance)}>
                    Remove
                  </button>
                  {target?.kind === 'check' && target.index === i ? (
                    <p class="note error" role="alert">
                      {message}
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
            {checksDirty ? (
              <button type="button" class="btn primary" disabled={busy || checks.some(checkTooLong)} onClick={() => void save('checks', checks, guidance)}>
                Save Checks
              </button>
            ) : null}
            <div class="row-field">
              <input type="text" aria-label="New Check" value={newCheck} disabled={busy} onInput={(e) => setNewCheck((e.target as HTMLInputElement).value)} />
              <span class={checkTooLong(newCheck) ? 'note error' : 'note'}>
                {runes(newCheck)}/{MAX_CHECK_RUNES}
              </span>
              {target?.kind === 'new' ? (
                <p class="note error" role="alert">
                  {message}
                </p>
              ) : null}
              <button type="button" class="btn" disabled={busy || !adding || checkTooLong(newCheck) || !canAddCheck(checks.length)} onClick={() => void save('checks', [...checks, adding], guidance)}>
                Add
              </button>
            </div>
          </section>

          <section class="orders-group" aria-labelledby="orders-guidance">
            <h2 id="orders-guidance" class="section-title">
              Guidance
            </h2>
            <p class="note">Added to the prompts of ships launched for outside events.</p>
            <textarea aria-label="Guidance" rows={5} value={guidance} disabled={busy} onInput={(e) => setGuidance((e.target as HTMLTextAreaElement).value)} />
            <span class={guidanceTooLong(guidance) ? 'note error' : 'note'}>
              {runes(guidance)}/{MAX_GUIDANCE_RUNES}
            </span>
            {target?.kind === 'guidance' ? (
              <p class="note error" role="alert">
                {message}
              </p>
            ) : null}
            <div class="routine-actions">
              <button type="button" class="btn primary" disabled={busy || !guidanceDirty || guidanceTooLong(guidance)} onClick={() => void save('guidance', checks, guidance)}>
                Save
              </button>
            </div>
          </section>

          <CopyTo fleet={fleet} onCopied={() => void load()} />
        </>
      ) : null}
    </main>
  );
}
