import { nextDelay, probe as probeShips, RETRY_FIRST_MS, signedOut, type Probe } from './connection';
import type { JoinMsg } from './bridgeReplay';

export type LinkState = 'connecting' | 'open' | 'reconnecting';

export interface LinkHandlers {
  state(s: LinkState): void;
  join(j: JoinMsg): void;
  output(bytes: Uint8Array): void;
  size(cols: number, rows: number): void;
  /** The ship is gone: the bridge closes. */
  gone(): void;
  /** The size to join with. */
  dims(): { cols: number; rows: number };
  /** What a failed reconnect asks to learn why: whether the session is still listed. Ships by default. */
  probe?: () => Promise<Probe>;
}

// One browser's WebSocket to a ship's bridge (GET /ws/bridge/<session>; the messages are in the
// README). It keeps nothing for the server that is away: input sent while it is down is dropped.
// When the socket drops it retries after 1 s, doubling to 10 s, and rejoins like a new browser.
export class BridgeLink {
  state: LinkState = 'connecting';
  private ws?: WebSocket;
  private retry?: ReturnType<typeof setTimeout>;
  private delay = RETRY_FIRST_MS;
  private stopped = false;

  constructor(
    private readonly session: string,
    private readonly h: LinkHandlers,
  ) {
    this.open();
  }

  private set(s: LinkState) {
    this.state = s;
    this.h.state(s);
  }

  private open() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/ws/bridge/${encodeURIComponent(this.session)}`);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    ws.onopen = () => {
      const { cols, rows } = this.h.dims();
      ws.send(JSON.stringify({ type: 'join', cols, rows }));
      // A tab that opens or reconnects hidden takes no stream until it is shown again.
      if (document.visibilityState !== 'visible') ws.send(JSON.stringify({ type: 'visible', visible: false }));
    };
    ws.onmessage = (e) => {
      if (typeof e.data !== 'string') return this.h.output(new Uint8Array(e.data));
      const msg = JSON.parse(e.data);
      if (msg.type === 'join') {
        this.delay = RETRY_FIRST_MS; // a server that accepts and then closes keeps backing off
        this.set('open');
        this.h.join(msg);
      } else if (msg.type === 'size') this.h.size(msg.cols, msg.rows);
    };
    ws.onclose = () => {
      if (this.stopped || this.ws !== ws) return;
      this.set('reconnecting');
      this.schedule();
    };
  }

  private schedule() {
    const wait = this.delay;
    this.delay = nextDelay(this.delay);
    this.retry = setTimeout(async () => {
      if (this.stopped || signedOut.value) return;
      const p = await (this.h.probe ?? probeShips)();
      if (this.stopped) return;
      if (p.kind === 'signed-out') signedOut.value = true;
      else if (p.kind === 'down') this.schedule();
      else if (!p.ships.some((s) => s.session === this.session)) this.h.gone();
      else this.open();
    }, wait);
  }

  /** Sends a message when the bridge is up; nothing is queued otherwise. */
  send(msg: object): void {
    if (this.state === 'open' && this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  close(): void {
    this.stopped = true;
    clearTimeout(this.retry);
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.close();
    }
  }
}
