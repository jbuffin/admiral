import { useEffect, useRef, useState } from 'preact/hooks';
import { signedOut } from './connection';
import { harnesses } from './harnesses';
import { KEYS, touchKeyboard } from './keys';
import { LogoFlag } from './Logos';
import { api, isWebUrl } from './sources';
import { useBridgePlacement } from './useBridgePlacement';
import { useBridgeTerminal } from './useBridgeTerminal';
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

const close = () => (boardedSession.value = null);

// Copies on a tap. An iPad on the Mac's plain-http LAN address has no navigator.clipboard (it needs a
// secure origin), so there a hidden text box is copied, still inside the tap.
function copyText(text: string): boolean {
  if (window.isSecureContext && navigator.clipboard) {
    void navigator.clipboard.writeText(text);
    return true;
  }
  const box = document.createElement('textarea');
  box.value = text;
  box.readOnly = true;
  box.style.cssText = 'position:fixed;top:0;left:0;opacity:0';
  document.body.append(box);
  box.select();
  box.setSelectionRange(0, text.length);
  const ok = document.execCommand('copy');
  box.remove();
  return ok;
}

// The command that attaches a real terminal to the ship's tmux session.
function AttachChip({ name }: { name: string }) {
  const command = `admiral attach ${name}`;
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <button type="button" class="term-attach" aria-label={`Copy ${command}`} onClick={() => setCopied(copyText(command))}>
      <span class="term-attach-text">{copied ? 'Copied' : command}</span>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linejoin="round" aria-hidden="true">
        <rect x="8" y="8" width="13" height="13" rx="2" />
        <path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" />
      </svg>
    </button>
  );
}

// Full view: the terminal takes the whole screen, over the header and the orders panel. Shared with
// the setup terminal.
export function FullViewToggle({ full, set }: { full: boolean; set: (on: boolean) => void }) {
  const label = full ? 'Exit full view' : 'Full view';
  return (
    <button type="button" class="press term-full" aria-label={label} title={label} aria-pressed={full} onClick={() => set(!full)}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        {full ? <path d="M9 3v6H3M15 3v6h6M9 21v-6H3M15 21v-6h6" /> : <path d="M3 9V3h6M21 9V3h-6M3 15v6h6M21 15v6h-6" />}
      </svg>
    </button>
  );
}

// A tap on the dimmed water around the terminal closes it, as the close button does. The press and the
// click must both land outside the terminal, so a text selection that ends outside keeps it open.
// Shared with the setup terminal.
export function useCloseOutside(close: () => void) {
  const pressedOutside = useRef(false);
  const outside = (e: MouseEvent) => !(e.target as Element).closest('.terminal, button');
  return {
    onPointerDown: (e: PointerEvent) => (pressedOutside.current = outside(e)),
    onClick: (e: MouseEvent) => pressedOutside.current && outside(e) && close(),
  };
}

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

  const [full, setFull] = useState(false);
  const [dismissError, setDismissError] = useState('');
  const dismissing = useRef(false);
  const root = useRef<HTMLDivElement>(null);
  const closeOutside = useCloseOutside(close);

  const status = ship?.status;
  const open = !!ship && live(ship);
  const { host, link, linkState, activity } = useBridgeTerminal({ session, open, gone: close });

  useBridgePlacement(root, !!ship, full);

  if (!ship) return null;
  const h = harnesses[ship.harness];
  const pill = PILL[ship.status];
  const reconnecting = linkState === 'reconnecting';
  // Anything typed or pressed makes this browser the one that sizes the pane.
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };
  // The server drops the bridge socket when the ship goes, so the link is closed as the request goes
  // out. A refusal leaves the read-only last screen up with the server's message.
  const onDismiss = async () => {
    if (dismissing.current) return;
    dismissing.current = true;
    setDismissError('');
    link.current?.close();
    const r = await api('POST', `/api/ships/${encodeURIComponent(session)}/dismiss`);
    dismissing.current = false;
    if (r.ok) close();
    else setDismissError(r.error);
  };
  const sub = [h.label, ship.fleet_name, ship.skill, ship.repo].filter(Boolean).join(' · ');
  const canType = open && linkState === 'open';

  return (
    <div class={full ? 'bridge full' : 'bridge'} ref={root} {...closeOutside}>
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
            {isWebUrl(ship.url) && (
              <a class="term-order-id" href={ship.url} target="_blank" rel="noopener noreferrer">
                {ship.order_id}
              </a>
            )}
            {open && <AttachChip name={ship.name} />}
          </div>
          {signedOut.value ? (
            <span class="term-notice">Open the URL Admiral printed</span>
          ) : (
            reconnecting && <span class="term-notice">Reconnecting…</span>
          )}
          <span class="term-pill" style={`background-color:${pill.color}`}>
            {pill.text(ship)}
          </span>
          <FullViewToggle full={full} set={setFull} />
          <button type="button" class="press term-close" aria-label="Close terminal and return to open water" onClick={close}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>

        <div class={reconnecting ? 'term-body dim' : 'term-body'}>
          <div class="term-host" ref={host} />
        </div>

        {status === 'anchored' ? (
          <div class="term-keys">
            <button type="button" class="press term-dismiss" onClick={onDismiss}>
              Dismiss
            </button>
            {dismissError && (
              <span class="term-note" role="alert">
                {dismissError}
              </span>
            )}
            <span class="term-grow" />
            <span class="term-note">The harness exited. This is its last screen.</span>
          </div>
        ) : (
          open &&
          touchKeyboard() && (
            <div class="term-keys">
              {KEYS.map(([name, label, bytes]) => (
                <button type="button" class="term-key" aria-label={`Send ${name}`} disabled={!canType} onClick={() => input(bytes)}>
                  {label}
                </button>
              ))}
              <span class="term-grow" />
              <span class="term-note">Keys go straight to the session</span>
            </div>
          )
        )}
      </section>
    </div>
  );
}
