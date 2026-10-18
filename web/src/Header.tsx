import { signal } from '@preact/signals';
import { useEffect, useRef } from 'preact/hooks';
import { AdmiralMark } from './AdmiralMark';
import { toggleSound, muted } from './chime';
import { config } from './config';
import { offline, signedOut } from './connection';
import { openSetup } from './setup';
import { atSea, board, needsYou, ships, shipsLoaded } from './ships';

const aboutOpen = signal(false);

function outsideBody(dialog: HTMLDialogElement | null, e: MouseEvent) {
  const r = dialog?.getBoundingClientRect();
  return !!r && (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom);
}

function About() {
  const ref = useRef<HTMLDialogElement>(null);
  const pressedOutside = useRef(false);
  useEffect(() => {
    const d = ref.current;
    if (d && aboutOpen.value && !d.open) d.showModal();
  }, [aboutOpen.value]);
  return (
    <dialog
      ref={ref}
      class="about"
      aria-label="About Admiral"
      onClose={() => (aboutOpen.value = false)}
      // Closes only when the press and the click both land outside the dialog's
      // body, so a tap on its border or a drag that ends outside stays open.
      onPointerDown={(e) => (pressedOutside.current = outsideBody(ref.current, e))}
      onClick={(e) => pressedOutside.current && outsideBody(ref.current, e) && ref.current?.close()}
    >
      <div class="about-body">
        <h2>About Admiral</h2>
        <p>
          Admiral is not affiliated with or endorsed by Anthropic, OpenAI or GitHub. Claude, Codex, Copilot and their
          logos are trademarks of their owners.
        </p>
        <button type="button" class="press about-close" onClick={() => ref.current?.close()}>
          Close
        </button>
      </div>
    </dialog>
  );
}

// The banner slot sits under the header's row: the connection banner above the config error.
function Banners() {
  const error = config.value?.error;
  return (
    <>
      {(signedOut.value || offline.value) && (
        <div class="banner" role="alert">
          {signedOut.value ? 'Open the URL Admiral printed' : 'Reconnecting…'}
        </div>
      )}
      {error && (
        <div class="banner" role="alert">
          <strong class="banner-file" title={error.file}>
            {error.file}
          </strong>
          <span>{error.message}</span>
        </div>
      )}
    </>
  );
}

function SeaPills() {
  const asking = needsYou.value;
  const off = muted.value;
  return (
    <>
      {shipsLoaded.value && (
        <span class="pill pill-lit">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#6FE3C8" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
            <path d="M3 17c2 0 2-1.5 4.5-1.5S9.5 17 12 17s2.5-1.5 4.5-1.5S19 17 21 17" />
            <path d="M6 13l6-9 6 9z" />
          </svg>
          {ships.value.filter(atSea).length} at sea
        </span>
      )}
      {asking.length > 0 && (
        <button type="button" class="pill-hit" onClick={() => board(asking[0].session)}>
          <span class="pill-face needs ping">{asking.length} needs you</span>
        </button>
      )}
      <button type="button" class="pill-hit" aria-label={off ? 'Turn chime on' : 'Mute chime'} onClick={toggleSound}>
        <span class="pill-face sound">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#F4EEDF" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M4 9h4l5-4v14l-5-4H4z" />
            <path d={off ? 'M16 9l5 6M21 9l-5 6' : 'M16.5 8.5a5 5 0 0 1 0 7M19 6a8.5 8.5 0 0 1 0 12'} />
          </svg>
          {off ? 'Muted' : 'Sound on'}
        </span>
      </button>
    </>
  );
}

export function Header() {
  const n = config.value?.fleets.length;
  return (
    <header class="app-header">
      <div class="header-row">
        <div class="brand">
          <AdmiralMark />
          <div class="app-title">
            <span class="app-name">Admiral</span>
            <span class="app-subtitle">Fleet command for coding agents</span>
          </div>
        </div>
        <div class="header-pills">
          <SeaPills />
          {n !== undefined && (
            <button type="button" class="pill-hit fleet-count" onClick={() => void openSetup()}>
              <span class="pill">
                <span class="dot" />
                {n === 1 ? '1 fleet' : `${n} fleets`}
              </span>
            </button>
          )}
          <button type="button" class="press about-button" aria-label="About Admiral" onClick={() => (aboutOpen.value = true)}>
            <span class="about-face">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#F4EEDF" stroke-width="2.2" stroke-linecap="round" aria-hidden="true">
                <circle cx="12" cy="12" r="9" />
                <path d="M12 11v6M12 7.5v.5" />
              </svg>
            </span>
          </button>
        </div>
      </div>
      <Banners />
      <About />
    </header>
  );
}
