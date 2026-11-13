import { useEffect } from 'preact/hooks';
import { Flag } from './Flag';
import { harnessName } from './harnessName';
import { MissingCapability } from './MissingCapability';
import type { Capability, Fleet, HarnessCapabilities as Harness, JudgeHarness } from './api';
import { capabilitiesFailed, capabilitiesLoaded, capabilities, checkErrors, checkKey, checking, loadCapabilities, recheck } from './capabilities';

const STATUS: Record<Capability['status'], string> = { passed: 'passed', failed: 'failed', not_checked: 'not checked' };

function HarnessBox({ fleet, h }: { fleet: string; h: Harness }) {
  const key = checkKey(fleet, h.harness);
  const busy = !!checking.value[key];
  const error = checkErrors.value[key];
  const id = `cap-${fleet}-${h.harness}`;
  return (
    <div class="box" role="group" aria-labelledby={id}>
      <div class="harness-head">
        <Flag harness={h.harness} size="header" />
        <h2 id={id}>{harnessName(h.harness)}</h2>
        <span class="muted">{h.version ? `version ${h.version}` : 'version not detected'}</span>
        <span class="muted" aria-live="polite">{busy ? 'Checking…' : ''}</span>
        <button type="button" class="btn" disabled={busy} aria-label={`Check again ${harnessName(h.harness)}`} onClick={() => void recheck(fleet, h.harness)}>
          Check again
        </button>
      </div>
      {error ? <p class="alert" role="alert">{error}</p> : null}
      {h.features.map((f) => (
        <div key={f.id} class="feature">
          <h3>{f.label}</h3>
          <ul class="set-list">
            {f.capabilities.map((c) => (
              <li key={c.id} class="set">
                <div class="set-text">
                  <strong>{c.label}</strong>
                  {c.detail ? <span class="muted">{c.detail}</span> : null}
                  {c.status === 'failed' ? <MissingCapability fleet={fleet} harness={h.harness} capability={c.label} /> : null}
                </div>
                <span class={`status status-${c.status}`}>{STATUS[c.status]}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

/** Judge calls stay visible when off, greyed out with the reason. */
function JudgeLine({ judge }: { judge: JudgeHarness }) {
  if (judge.harness) {
    return <p class="line judge-line">Judge calls: {harnessName(judge.harness)} ({judge.lookout_model} / {judge.strong_model})</p>;
  }
  return (
    <p class="line judge-line off">
      Judge calls: off{judge.reason ? `. ${judge.reason}` : ''}
    </p>
  );
}

/** A fleet's harnesses and what each was probed to do. */
export function HarnessCapabilities({ fleet }: { fleet: Fleet }) {
  useEffect(() => {
    void loadCapabilities();
    const on = () => document.visibilityState === 'visible' && void loadCapabilities();
    document.addEventListener('visibilitychange', on);
    return () => document.removeEventListener('visibilitychange', on);
  }, []);
  const current = capabilities.value.find((f) => f.key === fleet.key);
  const harnesses = current?.harnesses ?? [];
  return (
    <section class="caps" aria-label="Harness capabilities">
      <p class="line">What each harness was probed to do in this fleet. Features that need a capability stay off until it passes.</p>
      {capabilitiesFailed.value ? <p class="alert" role="alert">Couldn't reach Admiral. Try again.</p> : null}
      {!capabilitiesLoaded.value ? <p class="line">Loading…</p> : null}
      {capabilitiesLoaded.value && !capabilitiesFailed.value && harnesses.length === 0 ? <p class="line">No harnesses set up in this fleet.</p> : null}
      {current?.judge ? <JudgeLine judge={current.judge} /> : null}
      {harnesses.map((h) => (
        <HarnessBox key={h.harness} fleet={fleet.key} h={h} />
      ))}
    </section>
  );
}
