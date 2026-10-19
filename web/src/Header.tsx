import { signal } from '@preact/signals';
import { useEffect, useRef } from 'preact/hooks';
import { AdmiralMark } from './AdmiralMark';
import { config } from './config';

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

// The banner slot sits under the header's row. It holds one message: the
// config error now; u11 puts the reconnect banner above it.
function Banners() {
  const error = config.value?.error;
  if (!error) return null;
  return (
    <div class="banner" role="alert">
      <strong class="banner-file" title={error.file}>
        {error.file}
      </strong>
      <span>{error.message}</span>
    </div>
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
          {n !== undefined && (
            <span class="pill">
              <span class="dot" />
              {n === 1 ? '1 fleet' : `${n} fleets`}
            </span>
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
