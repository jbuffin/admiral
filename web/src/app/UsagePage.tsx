import { useEffect, useState } from 'preact/hooks';
import { fleetPaint, fleets } from './fleets';
import { Icon } from './Icon';
import { fleetRows, quotaCards, usagePeriods, type FleetUsage, type HarnessLine, type PeriodId } from './fleetUsage';
import { watchPath } from './shipUsage';

/** The JSON at `path`, read on open, every 30 s and when the page turns visible; a failed read keeps what was shown. */
function useUsage<T>(path: string): { data: T | null; error: string } {
  const [state, setState] = useState<{ data: T | null; error: string }>({ data: null, error: '' });
  useEffect(() => {
    setState({ data: null, error: '' });
    return watchPath<T>(path, (r) => {
      if (r.ok) setState({ data: r.data, error: '' });
      else setState((s) => ({ ...s, error: r.status === 404 ? 'No usage for this fleet.' : "Couldn't read usage." }));
      return r.ok;
    });
  }, [path]);
  return state;
}

function Line({ h }: { h: HarnessLine }) {
  const [open, setOpen] = useState(false);
  const figures = (
    <>
      <span class="usage-harness">{h.harness}</span>
      {h.tokens ? <span class="usage-tokens">{h.tokens}</span> : null}
      {h.money ? <span class="usage-money">{h.money}</span> : null}
      {h.incomplete ? <span class="usage-note">incomplete</span> : null}
    </>
  );
  if (h.spenders.length === 0) return <div class="usage-line">{figures}</div>;
  return (
    <details class="usage-split" onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary class="usage-line">
        <span class={`fold-chevron${open ? ' open' : ''}`}>
          <Icon name="chevron" size={14} />
        </span>
        {figures}
      </summary>
      <ul>
        {h.spenders.map((s) => (
          <li key={s.label} class="usage-line">
            <span class="usage-spender">{s.label}</span>
            {s.tokens ? <span class="usage-tokens">{s.tokens}</span> : null}
            {s.money ? <span class="usage-money">{s.money}</span> : null}
            {s.incomplete ? <span class="usage-note">incomplete</span> : null}
          </li>
        ))}
      </ul>
    </details>
  );
}

const Nothing = () => <p class="usage-empty">Nothing yet.</p>;

export function FleetUsagePage({ fleetKey, phone }: { fleetKey: string; phone: boolean }) {
  const { data, error } = useUsage<FleetUsage>(`/usage?fleet=${encodeURIComponent(fleetKey)}`);
  const Sub = phone ? 'h3' : 'h2';
  if (!data) return error ? <p class="usage-empty">{error}</p> : null;
  const cards = quotaCards(data.quota ?? [], new Date());
  return (
    <div class="usage">
      {usagePeriods(data).map((p) => (
        <section key={p.label} class="usage-period" aria-labelledby={`usage-${p.label}`}>
          <Sub id={`usage-${p.label}`}>{p.label}</Sub>
          {p.harnesses.length ? p.harnesses.map((h) => <Line key={h.harness} h={h} />) : <Nothing />}
        </section>
      ))}
      {cards.length ? (
        <div class="usage-quota">
          <Sub>Plan quota</Sub>
          {cards.map((c) => (
            <section key={c.label} class="usage-card" aria-labelledby={`quota-${c.label}`}>
              <h4 id={`quota-${c.label}`}>{c.label}</h4>
              <span class="usage-fresh">{c.fresh}</span>
              {c.bars.map((b) => (
                <div key={b.label} class="usage-bar">
                  <div class="usage-bar-head">
                    <span>{b.label}</span>
                    <span>{b.percent}%</span>
                    {b.note ? <span class="usage-note">{b.note}</span> : null}
                  </div>
                  <div class="progress" role="img" aria-label={b.aria}>
                    <span style={{ width: `${Math.min(100, Math.max(0, b.percent))}%` }} />
                  </div>
                  <div class="usage-bar-foot">{[b.detail, b.resets].filter(Boolean).join(' · ')}</div>
                </div>
              ))}
            </section>
          ))}
        </div>
      ) : null}
      {error ? <p class="usage-empty">{error}</p> : null}
    </div>
  );
}

const PERIODS: { id: PeriodId; label: string }[] = [
  { id: 'today', label: 'Today' },
  { id: '7d', label: '7 days' },
  { id: '30d', label: '30 days' },
];

export function AllUsagePage({ phone }: { phone: boolean }) {
  const { data, error } = useUsage<{ fleets: FleetUsage[] | null }>('/usage');
  const [period, setPeriod] = useState<PeriodId>('today');
  const Sub = phone ? 'h3' : 'h2';
  const rows = data ? fleetRows(data.fleets ?? [], period) : [];
  return (
    <div class="usage">
      <div class="usage-periods" role="group" aria-label="Period">
        {PERIODS.map((p) => (
          <button key={p.id} type="button" class="btn" aria-pressed={period === p.id} onClick={() => setPeriod(p.id)}>
            {p.label}
          </button>
        ))}
      </div>
      {rows.map((r) => {
        const f = fleets.value.find((x) => x.key === r.key);
        return (
          <section key={r.key} class="usage-period" aria-labelledby={`usage-fleet-${r.key}`}>
            <Sub id={`usage-fleet-${r.key}`}>
              {f ? <span class="dot" style={{ background: fleetPaint(f).fill }} aria-hidden="true" /> : null}
              {r.name}
            </Sub>
            {r.harnesses.length ? r.harnesses.map((h) => <Line key={h.harness} h={h} />) : <Nothing />}
          </section>
        );
      })}
      {error ? <p class="usage-empty">{error}</p> : null}
    </div>
  );
}
