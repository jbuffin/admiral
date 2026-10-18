import { useEffect, useRef, useState } from 'preact/hooks';
import type { Term } from './terminal';
import { BridgeLink, type LinkState } from './bridgeLink';
import { signedOut } from './connection';
import { harnesses } from './harnesses';
import { LogoFlag } from './Logos';
import { api } from './sources';
import { botFor, boardedSession, leaving, ships, shipsLoaded, type WireShip } from './ships';

const live = (s: WireShip) => s.status === 'underway' || s.status === 'needs_you';

const PILL = {
  underway: { text: () => 'Underway', color: '#6FE3C8' },
  needs_you: { text: () => 'Needs you', color: '#FF7A59' },
  anchored: {
    text: (s: WireShip) => (s.exit_signal ? `Anchored · killed (${s.exit_signal})` : `Anchored · exit ${s.exit_code ?? '?'}`),
    color: '#FF9A7A',
  },
  sailing_off: { text: () => 'Sailing off', color: '#6FE3C8' },
};

// What the key buttons send. The labels and the accessible names are the prototype's.
const KEYS = [
  ['Enter', '⏎ Enter', '\r'],
  ['Esc', 'Esc', '\x1b'],
  ['Tab', 'Tab', '\t'],
  ['Up', '↑', '\x1b[A'],
  ['Down', '↓', '\x1b[B'],
  ['Ctrl-C', '^C', '\x03'],
] as const;

// How long the line's text goes ahead of its Enter, so a TUI doesn't take the two as one paste.
const ENTER_GAP_MS = 50;

const close = () => (boardedSession.value = null);

export function Bridge() {
  const session = boardedSession.value;
  return session ? <BridgeView key={session} session={session} /> : null;
}

function BridgeView({ session }: { session: string }) {
  const ship = ships.value.find((s) => s.session === session) ?? leaving.value.get(session);
  // A ship that sailed off or is gone ends its bridge (a sailing-off ship's animation plays in open water).
  const over = shipsLoaded.value && (!ship || ship.status === 'sailing_off');
  useEffect(() => {
    if (over) close();
  }, [over]);

  const host = useRef<HTMLDivElement>(null);
  const term = useRef<Term | undefined>(undefined);
  const link = useRef<BridgeLink | undefined>(undefined);
  const active = useRef(true);
  const activity = useRef<() => void>(() => {});
  const [linkState, setLinkState] = useState<LinkState>('connecting');
  const [draft, setDraft] = useState('');
  const [dismissError, setDismissError] = useState('');

  const status = ship?.status;
  const open = !!ship && live(ship);
  const openNow = useRef(open);
  openNow.current = open;
  useEffect(() => term.current?.setStdin(open), [open]);

  useEffect(() => {
    let disposed = false;
    let observer: ResizeObserver | undefined;
    const claim = () => {
      // This browser sets the pane's size from now on.
      active.current = true;
      const d = term.current?.fit();
      if (d) link.current?.send({ type: 'resize', cols: d.cols, rows: d.rows });
    };
    const onActivity = () => {
      if (!active.current) claim();
      else link.current?.send({ type: 'active' });
    };
    activity.current = onActivity;
    const onVisibility = () => link.current?.send({ type: 'visible', visible: document.visibilityState === 'visible' });
    void import('./terminal').then(async ({ createTerminal }) => {
      if (disposed || !host.current) return;
      const t = await createTerminal(host.current);
      if (disposed) return t.dispose();
      term.current = t;
      t.setStdin(openNow.current);
      t.fit();
      if (window.__admiral) window.__admiral.screen = () => t.text();
      t.onData((data) => {
        onActivity();
        link.current?.send({ type: 'input', data });
      });
      link.current = new BridgeLink(session, {
        state: setLinkState,
        join: (j) => {
          // A live ship is sized to this browser when it joins; a ship that has exited keeps its own grid.
          const ours = t.size();
          active.current = j.size.cols === ours.cols && j.size.rows === ours.rows;
          t.join(j);
          if (!active.current) t.follow(j.size.cols, j.size.rows);
        },
        output: (b) => t.write(b),
        size: (cols, rows) => {
          const ours = t.size();
          if (cols === ours.cols && rows === ours.rows && active.current) return;
          // Another browser sized the pane: follow its grid and scale it, don't reflow.
          active.current = false;
          t.follow(cols, rows);
        },
        gone: close,
        dims: () => t.size(),
      });
      observer = new ResizeObserver(() => (active.current ? claim() : t.follow(t.size().cols, t.size().rows)));
      observer.observe(host.current);
    });
    const region = host.current?.closest('section');
    region?.addEventListener('pointerdown', onActivity);
    window.addEventListener('resize', claim);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      disposed = true;
      observer?.disconnect();
      region?.removeEventListener('pointerdown', onActivity);
      window.removeEventListener('resize', claim);
      document.removeEventListener('visibilitychange', onVisibility);
      link.current?.close();
      link.current = undefined;
      if (window.__admiral) delete window.__admiral.screen;
      term.current?.dispose();
      term.current = undefined;
    };
  }, [session]);

  if (!ship) return null;
  const h = harnesses[ship.harness];
  const pill = PILL[ship.status];
  const reconnecting = linkState === 'reconnecting';
  // Anything typed or pressed makes this browser the one that sizes the pane.
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };
  const submit = (e: Event) => {
    e.preventDefault();
    const text = draft;
    setDraft('');
    if (text) {
      input(text);
      setTimeout(() => input('\r'), ENTER_GAP_MS);
    } else input('\r');
  };
  const sub = [h.label, ship.fleet_name, ship.skill, ship.repo].filter(Boolean).join(' · ');
  const canType = open && linkState === 'open';

  return (
    <div class="bridge">
      <nav class="flotilla" aria-label="Session flotilla">
        <span class="flotilla-title">Flotilla · 1</span>
        <button type="button" class="fl-item press">
          <svg width="52" height="50" viewBox="0 0 100 96" fill="none" aria-hidden="true">
            <path d="M50 64 L50 3" stroke="#14304A" stroke-width="5" stroke-linecap="round" />
            <path
              d="M36 14 L64 14 Q68 23 64 32 L36 32 Q40 23 36 14 Z M31 36 L69 36 Q75 47 69 58 L31 58 Q36 47 31 36 Z"
              fill="#FFFDF6"
              stroke="#14304A"
              stroke-width="3.5"
              stroke-linejoin="round"
            />
            <LogoFlag harness={ship.harness} kind="sea" />
            <rect x="75" y="33" width="18" height="18" rx="3" fill={botFor(ship.session)} stroke="#14304A" stroke-width="3.2" />
            <path d="M4 62 L98 62 L93 76 Q86 86 70 86 L28 86 Q12 84 4 62 Z" fill="#A0613A" stroke="#14304A" stroke-width="4" stroke-linejoin="round" />
            <path d="M7 67 L96 67 L94 73.5 L9.5 73.5 Z" fill={ship.fleet_color} />
          </svg>
          <span class="fl-text">
            <span class="fl-name">{ship.name}</span>
            <span class="fl-role">{ship.skill ? `${ship.skill} · lead` : 'lead'}</span>
          </span>
        </button>
      </nav>

      <section class="terminal" aria-label="Attached terminal">
        <div class="term-head">
          <svg width="40" height="26" viewBox="0 0 40 26" aria-hidden="true">
            <LogoFlag harness={ship.harness} kind="bridge" />
          </svg>
          <div class="term-title">
            <span class="term-name">{ship.name}</span>
            <span class="term-sub">{sub}</span>
            {ship.title && <span class="term-order">{ship.title}</span>}
          </div>
          {signedOut.value ? (
            <span class="term-notice">Open the URL Admiral printed</span>
          ) : (
            reconnecting && <span class="term-notice">Reconnecting…</span>
          )}
          <span class="term-pill" style={`background-color:${pill.color}`}>
            {pill.text(ship)}
          </span>
          <button type="button" class="press term-close" aria-label="Close terminal and return to open water" onClick={close}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>

        <div class={reconnecting ? 'term-body dim' : 'term-body'}>
          <div class="term-host" ref={host} />
          {open && (
            <form class="term-prompt" onSubmit={submit}>
              <label for="adm-cmd" class="sr-only">
                Input to {ship.name}
              </label>
              <span aria-hidden="true" class="term-caret">
                ❯
              </span>
              <input
                id="adm-cmd"
                class="term-in"
                value={draft}
                disabled={!canType}
                onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
                autocomplete="off"
                autocapitalize="off"
                spellcheck={false}
                placeholder="type here and press Enter"
              />
            </form>
          )}
        </div>

        <div class="term-keys">
          {status === 'anchored' ? (
            <>
              <button type="button" class="press term-dismiss" onClick={() => dismiss(session, setDismissError)}>
                Dismiss
              </button>
              {dismissError && (
                <span class="term-note" role="alert">
                  {dismissError}
                </span>
              )}
              <span class="term-grow" />
              <span class="term-note">The harness exited. This is its last screen.</span>
            </>
          ) : (
            open && (
              <>
                {status === 'needs_you' && (
                  <>
                    <button type="button" class="press term-yes" disabled={!canType} onClick={() => input('y')}>
                      Approve · y
                    </button>
                    <button type="button" class="press term-no" disabled={!canType} onClick={() => input('n')}>
                      Deny · n
                    </button>
                    <span class="term-sep" aria-hidden="true" />
                  </>
                )}
                {KEYS.map(([name, label, bytes]) => (
                  <button type="button" class="term-key" aria-label={`Send ${name}`} disabled={!canType} onClick={() => input(bytes)}>
                    {label}
                  </button>
                ))}
                <span class="term-grow" />
                <span class="term-note">Keys go straight to the session</span>
              </>
            )
          )}
        </div>
      </section>
    </div>
  );
}

// Dismissing an anchored ship ends its session; the ship is gone and the bridge closes. A refusal
// leaves the bridge open with the server's message.
async function dismiss(session: string, fail: (message: string) => void): Promise<void> {
  const r = await api('POST', `/api/ships/${encodeURIComponent(session)}/dismiss`);
  if (r.ok) close();
  else fail(r.error);
}
