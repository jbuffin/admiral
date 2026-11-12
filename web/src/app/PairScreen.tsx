import { useState } from 'preact/hooks';
import { call } from './api';
import { markPaired, pairHeading, wasPaired } from './pairing';

export function PairScreen() {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const heading = pairHeading(wasPaired());

  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError('');
    // The server sets the device cookie itself, so the token never reaches this script.
    const res = await call('POST', '/pair', { code, cookie: true });
    if (res.ok) {
      markPaired();
      location.reload();
      return;
    }
    setError(res.error);
    setBusy(false);
  };

  return (
    <div class="unreachable pair">
      <form class="unreachable-card" onSubmit={submit}>
        <h1>{heading}</h1>
        <p class="pair-help">
          On your Mac, run <code>admiral pair</code> and type the code it shows.
        </p>
        <label class="pair-label" for="pair-code">
          Pairing code
        </label>
        <input
          id="pair-code"
          class="pair-code"
          type="text"
          value={code}
          onInput={(e) => setCode(e.currentTarget.value)}
          placeholder="XXXX-XXXX"
          autocomplete="off"
          autocapitalize="characters"
          autocorrect="off"
          spellcheck={false}
          enterkeyhint="go"
          required
        />
        {error ? (
          <p class="alert" role="alert">
            {error}
          </p>
        ) : null}
        <button type="submit" class="btn primary pair-go" disabled={busy}>
          Pair
        </button>
      </form>
    </div>
  );
}
