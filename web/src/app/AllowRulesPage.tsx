import { useEffect, useState } from 'preact/hooks';
import { Button } from './Button';
import { Flag } from './Flag';
import { harnessName } from './harnessName';
import { getAllowRules, removeAllowRule, unskipAllowRule, type AllowRule, type AllowRules, type Fleet, type Result } from './api';

export const ruleText = (r: AllowRule) => r.rule || (r.pattern ?? []).join(' ');
const ruleKey = (r: AllowRule) => JSON.stringify([r.harness, r.repo ?? '', r.rule ?? '', r.pattern ?? []]);

function addedOn(ts?: string): string {
  const t = ts ? Date.parse(ts) : NaN;
  return Number.isNaN(t) ? '' : `Added ${new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}`;
}

function Rows({ label, list, busy, kind, onRemove }: { label: string; list: AllowRule[]; busy: boolean; kind: 'rule' | 'skip'; onRemove: (r: AllowRule) => void }) {
  return (
    <ul class="set-list" aria-label={label}>
      {list.map((r) => {
        const key = ruleKey(r);
        return (
          <li key={key} class="set note-row allow-rule">
            <Flag harness={r.harness} size="row" label={harnessName(r.harness)} />
            <div class="t">
              <code class="label set-text">{ruleText(r)}</code>
              <span>
                {harnessName(r.harness)} · {r.harness === 'codex' ? 'every repo' : r.repo}
                {addedOn(r.added) ? ` · ${addedOn(r.added)}` : ''}
              </span>
            </div>
            <div class="note-actions">
              <Button label={`Remove ${kind === 'rule' ? 'rule' : 'skip'} ${ruleText(r)}`} disabled={busy} pending={['allow-rule', key, kind]} onClick={() => onRemove(r)}>
                Remove
              </Button>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

/** A fleet's allow rules and skipped proposals. Rules come only from the Inbox, so this page only removes. */
export function AllowRulesPage({ fleet }: { fleet: Fleet }) {
  const [shown, setShown] = useState<AllowRules | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = async () => {
    const r = await getAllowRules(fleet.key);
    if (r.ok) setShown(r.data);
    setError(r.ok ? '' : r.error);
  };
  useEffect(() => {
    void load();
    const on = () => document.visibilityState === 'visible' && void load();
    document.addEventListener('visibilitychange', on);
    return () => document.removeEventListener('visibilitychange', on);
  }, [fleet.key]);

  const remove = async (run: () => Promise<Result<AllowRules>>) => {
    setBusy(true);
    setError('');
    const r = await run();
    if (r.ok) setShown(r.data);
    else {
      await load();
      setError(r.error);
    }
    setBusy(false);
  };

  return (
    <section aria-label="Allow rules">
      <p class="line">Calls this fleet's ships run without asking. A rule is added only when you choose Add on its proposal in the Inbox.</p>
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      {!shown ? (
        !error ? <p class="line">Loading…</p> : null
      ) : (
        <>
          <h2 class="section-title">Rules</h2>
          <div class="box">
            <div class="set">
              <div class="t">
                <span>Remove a rule and the next ship asks again. A Codex rule covers every repo in the fleet.</span>
              </div>
            </div>
            {shown.rules.length === 0 ? (
              <div class="set">
                <div class="t">
                  <span>No allow rules yet.</span>
                </div>
              </div>
            ) : (
              <Rows label="Allow rules" list={shown.rules} busy={busy} kind="rule" onRemove={(r) => void remove(() => removeAllowRule(fleet.key, r))} />
            )}
          </div>
          <h2 class="section-title">Skipped proposals</h2>
          <div class="box">
            <div class="set">
              <div class="t">
                <span>Admiral won't propose these again. Remove a skip and it can.</span>
              </div>
            </div>
            {shown.skipped.length === 0 ? (
              <div class="set">
                <div class="t">
                  <span>No skipped proposals.</span>
                </div>
              </div>
            ) : (
              <Rows label="Skipped proposals" list={shown.skipped} busy={busy} kind="skip" onRemove={(r) => void remove(() => unskipAllowRule(fleet.key, r))} />
            )}
          </div>
        </>
      )}
    </section>
  );
}
