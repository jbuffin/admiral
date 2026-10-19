import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { joinSequence, type JoinMsg } from './bridgeReplay';
import { NEWLINE } from './keys';

// xterm.js with the fit addon and WebGL where the browser has it. This module is loaded when the
// first bridge opens, so the dashboard doesn't carry the terminal until then (story 103).

export interface Term {
  /** Draws what a joining browser is sent, replacing everything. */
  join(j: JoinMsg): void;
  write(bytes: Uint8Array): void;
  onData(fn: (data: string) => void): void;
  /** Whether keys typed into the terminal are accepted. */
  setStdin(on: boolean): void;
  /** Fits the terminal to its box and returns the grid, for the browser that sizes the pane. */
  fit(): { cols: number; rows: number } | undefined;
  /** Takes the server's grid and scales it into the box, never reflowing: for the other browsers. */
  follow(cols: number, rows: number): void;
  size(): { cols: number; rows: number };
  focus(): void;
  /** The active buffer as text, scrollback and screen, trailing blanks trimmed. */
  text(): string;
  dispose(): void;
}

export async function createTerminal(host: HTMLElement): Promise<Term> {
  // The grid is measured with the real font, so it has to be loaded before the first fit.
  await document.fonts.load('13.5px "JetBrains Mono"').catch(() => []);
  const cs = getComputedStyle(document.documentElement);
  const color = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback;
  const term = new Terminal({
    fontFamily: color('--font-mono', 'monospace'),
    fontSize: 13.5,
    lineHeight: 1.25,
    scrollback: 5000,
    cursorBlink: true,
    theme: {
      background: color('--color-terminal-bg', '#0E2235'),
      foreground: color('--color-terminal-agent', '#F4EEDF'),
      cursor: color('--color-terminal-you', '#FFC23D'),
      selectionBackground: '#3A6489',
    },
  });
  // xterm.js sends CR for Shift+Enter as for Enter, so the harness would submit. Send a new line instead.
  term.attachCustomKeyEventHandler((e) => {
    if (e.key !== 'Enter' || !e.shiftKey || e.altKey || e.ctrlKey || e.metaKey) return true;
    if (e.type === 'keydown') term.input(NEWLINE);
    e.preventDefault();
    return false;
  });
  const fitter = new FitAddon();
  term.loadAddon(fitter);
  // The box the terminal fills; the passive browser's grid is scaled inside it.
  const box = document.createElement('div');
  box.className = 'term-box';
  host.append(box);
  term.open(box);
  swipeToScroll(box);
  try {
    const gl = new WebglAddon();
    gl.onContextLoss(() => gl.dispose());
    term.loadAddon(gl);
  } catch {
    // No WebGL: xterm.js keeps its DOM renderer.
  }

  const scale = () => {
    const el = term.element;
    if (!el || !box.classList.contains('follow')) return;
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    const s = Math.min(1, host.clientWidth / w, host.clientHeight / h);
    box.style.transform = `scale(${Number.isFinite(s) && s > 0 ? s : 1})`;
  };

  const t: Term = {
    join(j) {
      term.reset();
      if (term.cols !== j.size.cols || term.rows !== j.size.rows) term.resize(j.size.cols, j.size.rows);
      term.write(joinSequence(j), scale);
    },
    write: (bytes) => term.write(bytes),
    onData: (fn) => void term.onData(fn),
    setStdin(on) {
      term.options.disableStdin = !on;
      // A terminal that takes no keys has no text box for assistive technology to offer.
      term.textarea?.setAttribute('aria-hidden', String(!on));
    },
    fit() {
      box.classList.remove('follow');
      box.style.transform = '';
      const d = fitter.proposeDimensions();
      if (!d || !Number.isFinite(d.cols) || !Number.isFinite(d.rows)) return undefined;
      if (d.cols !== term.cols || d.rows !== term.rows) term.resize(d.cols, d.rows);
      return { cols: term.cols, rows: term.rows };
    },
    follow(cols, rows) {
      box.classList.add('follow');
      if (term.cols !== cols || term.rows !== rows) term.resize(cols, rows);
      requestAnimationFrame(scale);
    },
    size: () => ({ cols: term.cols, rows: term.rows }),
    focus: () => term.focus(),
    text() {
      const b = term.buffer.active;
      const out: string[] = [];
      for (let i = 0; i < b.length; i++) out.push(b.getLine(i)?.translateToString(true) ?? '');
      return out.join('\n').replace(/\n+$/, '');
    },
    dispose() {
      term.dispose();
      box.remove();
    },
  };
  return t;
}

// xterm.js 6 scrolls on wheel events only, so a swipe on iPad does nothing. A one-finger drag is
// turned into wheel events, which xterm.js handles as it does a trackpad: scrollback, or arrows and
// mouse reports when the app has the alternate screen or mouse mode.
function swipeToScroll(box: HTMLElement) {
  let last: { id: number; y: number } | undefined;
  box.addEventListener(
    'touchstart',
    (e) => {
      const t = e.touches.length === 1 ? e.touches[0] : undefined;
      last = t && { id: t.identifier, y: t.clientY };
    },
    { passive: true },
  );
  box.addEventListener(
    'touchmove',
    (e) => {
      const t = last && Array.from(e.changedTouches).find((c) => c.identifier === last?.id);
      if (!last || !t || e.touches.length !== 1) return;
      const dy = last.y - t.clientY;
      last.y = t.clientY;
      e.preventDefault();
      if (dy === 0) return;
      const at = document.elementFromPoint(t.clientX, t.clientY) ?? box;
      at.dispatchEvent(
        new WheelEvent('wheel', {
          deltaY: dy,
          deltaMode: WheelEvent.DOM_DELTA_PIXEL,
          clientX: t.clientX,
          clientY: t.clientY,
          bubbles: true,
          cancelable: true,
        }),
      );
    },
    { passive: false },
  );
  box.addEventListener('touchend', () => (last = undefined));
  box.addEventListener('touchcancel', () => (last = undefined));
}
