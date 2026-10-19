import { useEffect, useRef, useState } from 'preact/hooks';
import { BridgeLink, type LinkState } from './bridgeLink';
import type { Probe } from './connection';
import type { Term } from './terminal';

export interface BridgeTerminalOptions {
  /** The session the bridge socket joins. */
  session: string;
  /** Whether keys typed into the terminal are accepted while the bridge is connected. */
  open: boolean;
  /** The session is gone: the caller closes its terminal. */
  gone(): void;
  /** What a failed reconnect asks: is the session still there? Ships by default. */
  probe?: () => Promise<Probe>;
}

/**
 * xterm.js in `host` joined to a session's bridge: this browser sizes the pane while it is the
 * active one and follows another's grid otherwise (ADR 0011). A ship's bridge and the setup
 * terminal both run on it.
 */
export function useBridgeTerminal({ session, open, gone, probe }: BridgeTerminalOptions) {
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<Term | undefined>(undefined);
  const link = useRef<BridgeLink | undefined>(undefined);
  const active = useRef(true);
  // Typing at the prompt or pressing a key button counts as activity like a tap or a keystroke does.
  const activity = useRef<() => void>(() => {});
  const [linkState, setLinkState] = useState<LinkState>('connecting');

  // Nothing typed while the link is down is queued, so the terminal takes no keys then either.
  const typeable = open && linkState === 'open';
  const openNow = useRef(typeable);
  openNow.current = typeable;
  // Boarding puts the cursor in the terminal the first time it takes keys, so typing starts right away.
  const focused = useRef(false);
  useEffect(() => {
    term.current?.setStdin(typeable);
    if (typeable && term.current && !focused.current) {
      focused.current = true;
      term.current.focus();
    }
  }, [typeable]);
  const goneNow = useRef(gone);
  goneNow.current = gone;

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
          // A live session is sized to this browser when it joins; one that has exited keeps its own grid.
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
        gone: () => goneNow.current(),
        dims: () => t.size(),
        probe,
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

  return { host, link, linkState, activity };
}
