import { useEffect, useRef, useState } from 'preact/hooks';
import { signedOut } from './connection';
import { attachCommand, crewRowText, findCrew, takesInput } from './crew';
import { harnesses } from './harnesses';
import { clock } from './inboxState';
import { KEYS, touchKeyboard } from './keys';
import { LogoFlag } from './Logos';
import { Menu, MenuItem } from './Menu';
import { Modal } from './Modal';
import { api, isWebUrl } from './sources';
import { useBridgePlacement } from './useBridgePlacement';
import { useBridgeTerminal } from './useBridgeTerminal';
import { board, boarded, botFor, leaving, ships, shipsLoaded, unboard, type WireCrew, type WireShip } from './ships';

const enc = encodeURIComponent;

const PILL = {
  underway: { text: () => 'Underway', color: '#6FE3C8' },
  needs_you: { text: () => 'Needs you', color: '#FF7A59' },
  anchored: {
    text: (s: { exit_code?: number | null; exit_signal?: string | null }) => (s.exit_signal ? `Anchored · killed (${s.exit_signal})` : `Anchored · exit ${s.exit_code ?? '?'}`),
    color: '#FF9A7A',
  },
  sailing_off: { text: () => 'Sailing off', color: '#6FE3C8' },
};

const close = unboard;
const backToShip = () => {
  if (boarded.value) boarded.value = { session: boarded.value.session };
};

// A subagent's age on the flotilla: under a minute is "just started"; a start that does not parse shows none.
function age(start: string, now: number): string {
  const t = Date.parse(start);
  if (Number.isNaN(t)) return '';
  const m = Math.max(0, Math.round((now - t) / 60000));
  return m < 1 ? 'just started' : `${m}m`;
}

const CHIP_BG = { underway: 'var(--color-foam)', needs_you: 'var(--color-attention-plaque)', anchored: 'var(--color-stone)' };
const CHIP_STATE = { underway: 'working', needs_you: 'needs you', anchored: 'anchored' };

// The small crew boat of the flotilla (designs/assets/crew-boat.svg, anchored: crew-boat-anchored.svg).
function CrewBoat({ boat, stripe, bot }: { boat: WireCrew; stripe: string; bot: string }) {
  const anchored = boat.status === 'anchored';
  return (
    <svg width="52" height="46" viewBox="0 0 64 56" fill="none" aria-hidden="true">
      <path d="M4 37 L60 37 L54 46 Q46 51 32 51 L18 51 Q8 49 4 37 Z" fill="#A0613A" stroke="#14304A" stroke-width="2.5" stroke-linejoin="round" />
      <path d="M6 40 L59 40 L57.5 44.5 L8 44.5 Z" fill={anchored ? '#9AA5AF' : stripe} />
      <path d="M30 37 L30 6" stroke="#14304A" stroke-width="3" stroke-linecap="round" />
      {anchored ? (
        <>
          <path d="M32 27 Q42 29 46 33 L32 33 Z" fill="#E4D6B8" stroke="#14304A" stroke-width="2" stroke-linejoin="round" />
          <path d="M22 31 L48 31" stroke="#14304A" stroke-width="2.4" stroke-linecap="round" />
        </>
      ) : (
        <path d="M32 9 L32 33 L52 33 Z" fill="#FFF6E3" stroke="#14304A" stroke-width="2.2" stroke-linejoin="round" />
      )}
      <LogoFlag harness={boat.harness} kind="crew" />
      <rect x="4" y="24" width="13" height="13" rx="2" fill={bot} stroke="#14304A" stroke-width="2" />
    </svg>
  );
}

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

// The command that attaches a real terminal to the ship's tmux session, or to one crew boat's window.
function AttachChip({ command }: { command: string }) {
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
// click must both land outside the terminal and the flotilla's content, so a text selection that ends outside
// keeps it open. Shared with the setup terminal.
export function useCloseOutside(close: () => void) {
  const pressedOutside = useRef(false);
  const outside = (e: MouseEvent) => !(e.target as Element).closest('.terminal, button, .flotilla-title, .flotilla-subtitle, .fl-sub');
  return {
    onPointerDown: (e: PointerEvent) => (pressedOutside.current = outside(e)),
    onClick: (e: MouseEvent) => pressedOutside.current && outside(e) && close(),
  };
}

export function Bridge() {
  const session = boarded.value?.session;
  return session ? <BridgeView key={session} session={session} /> : null;
}

function BridgeView({ session }: { session: string }) {
  const ship = ships.value.find((s) => s.session === session) ?? leaving.value.get(session);
  // A ship that sailed off or is gone ends its bridge (a sailing-off ship's animation plays in open water).
  const over = shipsLoaded.value && (!ship || ship.status === 'sailing_off');
  useEffect(() => {
    if (over) close();
  }, [over]);
  // The boarded crew boat, while the ship still lists it. One that is closed here or elsewhere sends the bridge
  // back to the ship's terminal.
  const crewName = boarded.value?.crew;
  const boat = crewName === undefined ? undefined : findCrew(ships.value, session, crewName);
  const crew = boat ? crewName : undefined;
  const crewGone = shipsLoaded.value && !!ship && crewName !== undefined && !boat;
  useEffect(() => {
    if (crewGone) backToShip();
  }, [crewGone]);

  const [full, setFull] = useState(false);
  const [actionError, setActionError] = useState('');
  const [confirming, setConfirming] = useState(false);
  const dismissing = useRef(false);
  const closingCrew = useRef(false);
  const root = useRef<HTMLDivElement>(null);
  const closeOutside = useCloseOutside(close);

  const open = !!ship && takesInput(ship, boat);
  const { host, link, linkState, activity } = useBridgeTerminal({
    session,
    crew,
    open,
    // A crew link is told the boat is gone, or the whole ship: the ship's own bridge closes with the ship.
    gone: crew ? () => (ships.value.some((s) => s.session === session) ? backToShip() : close()) : close,
  });
  // An error belongs to the terminal it came from.
  useEffect(() => setActionError(''), [crew]);

  useBridgePlacement(root, !!ship, full);

  if (!ship) return null;
  const h = harnesses[(boat ?? ship).harness];
  // The pill is the boarded boat's. A ship that needs you only because of its crew is still underway itself.
  const pill = boat
    ? PILL[boat.status]
    : ship.status === 'needs_you' && ship.reason === 'crew'
      ? PILL.underway
      : PILL[ship.status];
  const pillOf = boat ?? ship;
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
    setActionError('');
    link.current?.close();
    const r = await api('POST', `/api/ships/${enc(session)}/dismiss`);
    dismissing.current = false;
    if (r.ok) close();
    else setActionError(r.error);
  };
  // Live crew boats end with the ship, so Dismiss asks first.
  const onDismissTap = () => (ship.crew_live > 0 ? setConfirming(true) : onDismiss());
  const onCloseCrew = async () => {
    if (!crew || closingCrew.current) return;
    closingCrew.current = true;
    setActionError('');
    const r = await api('POST', `/api/ships/${enc(session)}/crew/${enc(crew)}/close`);
    closingCrew.current = false;
    if (r.ok) backToShip();
    else setActionError(r.error);
  };
  // The boat's dir is its own checkout, not the ship's repo, so the repo is the lead's alone.
  const sub = [h.label, ship.fleet_name, ship.skill, crew ? undefined : ship.repo].filter(Boolean).join(' · ');
  const canType = open && linkState === 'open';
  // Ships launched before @admiral-url have no url, but a pasted order's title is often the link itself.
  const orderHref = isWebUrl(ship.url) ? ship.url : isWebUrl(ship.title) && !/\s/.test(ship.title) ? ship.title : undefined;
  const live = ship.crew_live;
  const now = clock.value;

  return (
    <>
      <div class={full ? 'bridge full' : 'bridge'} ref={root} {...closeOutside}>
        <nav class="flotilla" aria-label="Session flotilla">
          <span class="flotilla-title">Flotilla · {1 + ship.crew.length}</span>
          <button type="button" class={crew ? 'fl-item press' : 'fl-item on press'} aria-current={crew ? undefined : 'true'} onClick={() => board(session)}>
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
          {ship.crew.map((c, k) => {
            const state = c.status === 'underway' ? 'working' : c.status;
            return (
              <button
                key={c.name}
                type="button"
                class={`fl-item press${c.name === crew ? ' on' : ''}${c.status === 'needs_you' ? ' attn attn-row' : ''}`}
                style={`animation-delay:${(k + 1) * 90}ms`}
                data-state={state}
                aria-current={c.name === crew ? 'true' : undefined}
                onClick={() => board(session, c.name)}
              >
                <CrewBoat boat={c} stripe={ship.fleet_color} bot={botFor(`${session}/${c.name}`)} />
                <span class="fl-text">
                  <span class="fl-name">{c.name}</span>
                  <span class="fl-role">{crewRowText(c)}</span>
                </span>
                {c.status === 'needs_you' && <span class="fl-badge fl-bang ping">!</span>}
                {c.status === 'anchored' && (
                  <span class="fl-badge fl-x">
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#FFFDF6" stroke-width="4.5" stroke-linecap="round" aria-hidden="true">
                      <path d="M6 6l12 12M18 6L6 18" />
                    </svg>
                  </span>
                )}
              </button>
            );
          })}
          {ship.subagents.length > 0 && (
            <>
              <span class="flotilla-subtitle">
                Subagents · {ship.subagents.length} <span class="flotilla-subtitle-note">· no terminal</span>
              </span>
              {ship.subagents.map((m) => (
                <div class="fl-sub" role="note" key={m.id}>
                  <svg width="18" height="18" viewBox="0 0 22 26" fill="none" aria-hidden="true">
                    <circle cx="11" cy="15" r="8" fill="#FFFDF6" stroke="#14304A" stroke-width="2.2" />
                    <path d="M3.6 13 L18.4 13 L18.6 17 L3.4 17 Z" fill={ship.fleet_color} />
                  </svg>
                  <b>{m.type}</b>
                  <span class="fl-sub-age">{age(m.start, now)}</span>
                </div>
              ))}
            </>
          )}
        </nav>

        <section class="terminal" aria-label="Attached terminal">
          <div class="term-head">
            <svg width="40" height="26" viewBox="0 0 40 26" aria-hidden="true">
              <LogoFlag harness={(boat ?? ship).harness} kind="bridge" />
            </svg>
            <div class="term-title">
              <span class="term-name">
                {ship.name}
                {crew && <span class="term-crew"> › {crew}</span>}
              </span>
              <span class="term-sub">{sub}</span>
              {ship.title &&
                (orderHref ? (
                  <a class="term-order" href={orderHref} target="_blank" rel="noopener noreferrer">
                    {ship.title}
                  </a>
                ) : (
                  <span class="term-order">{ship.title}</span>
                ))}
              {open && <AttachChip command={attachCommand(ship.name, crew)} />}
            </div>
            {signedOut.value ? (
              <span class="term-notice">Open the URL Admiral printed</span>
            ) : (
              reconnecting && <span class="term-notice">Reconnecting…</span>
            )}
            <span class="term-pill" style={`background-color:${pill.color}`}>
              {pill.text(pillOf)}
            </span>
            {crew && (
              <Menu id={`crew-${session}-${crew}`} label={`More for ${crew}`}>
                <MenuItem onSelect={onCloseCrew} disabled={boat?.status !== 'anchored'}>
                  Close crew boat
                </MenuItem>
                {boat?.status !== 'anchored' && (
                  <span class="menu-note" role="none">Still working. Its ship's skill closes it, or it anchors when its harness exits.</span>
                )}
              </Menu>
            )}
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

          {crew && !open ? (
            <div class="term-keys">
              {actionError && (
                <span class="term-note" role="alert">
                  {actionError}
                </span>
              )}
              <span class="term-grow" />
              <span class="term-note">This teammate's harness exited. Close its crew boat from ⋯.</span>
            </div>
          ) : !crew && ship.status === 'anchored' ? (
            <div class="term-keys">
              <button type="button" class="press term-dismiss" onClick={onDismissTap}>
                Dismiss
              </button>
              {actionError && (
                <span class="term-note" role="alert">
                  {actionError}
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
      {confirming && (
        <Modal label={`Dismiss ${ship.name}?`} role="alertdialog" onClose={() => setConfirming(false)}>
          <h3>Dismiss {ship.name}?</h3>
          <p class="confirm-text">
            <b>
              {live} crew {live === 1 ? 'boat is' : 'boats are'} still working.
            </b>{' '}
            Dismissing {ship.name} ends {live === 1 ? 'it' : 'them'} too. Their commits and reports stay on disk.
          </p>
          <div class="confirm-chips">
            {ship.crew.map((c) => (
              <span class="confirm-chip" key={c.name} style={`background-color:${CHIP_BG[c.status]}`}>
                {c.name} · {CHIP_STATE[c.status]}
              </span>
            ))}
          </div>
          <div class="confirm-actions">
            <button type="button" class="press confirm-keep" onClick={() => setConfirming(false)}>
              Keep it
            </button>
            <button
              type="button"
              class="press confirm-go"
              onClick={() => {
                setConfirming(false);
                void onDismiss();
              }}
            >
              Dismiss ship and crew
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
