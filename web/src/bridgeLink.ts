import { nextDelay, probe as probeShips, RETRY_FIRST_MS, signedOut, type Probe } from './connection';
import type { JoinMsg } from './bridgeReplay';
import { bridgePath } from './crew';

export type LinkState = 'connecting' | 'open' | 'reconnecting';

export interface LinkHandlers {
  state(s: LinkState): void;
  join(j: JoinMsg): void;
  output(bytes: Uint8Array): void;
  size(cols: number, rows: number): void;
  gone(): void;
  dims(): { cols: number; rows: number };
  /** A crew link lists the session only while its boat is still in the ship's `crew[]`. */
  probe?: () => Promise<Probe>;
}

// Input sent while the server is away is dropped; on a drop it retries after 1 s, doubling to 10 s, and rejoins like a new browser.
export class BridgeLink {
  state: LinkState = 'connecting';
  private ws?: WebSocket;
  private retry?: ReturnType<typeof setTimeout>;
  private delay = RETRY_FIRST_MS;
  private stopped = false;

  constructor(
    private readonly session: string,
    private readonly h: LinkHandlers,
    private readonly crew?: string,
  ) {
    this.open();
  }

  private set(s: LinkState) {
    this.state = s;
    this.h.state(s);
  }

  private open() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}${bridgePath(this.session, this.crew)}`);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    ws.onopen = () => {
      const { cols, rows } = this.h.dims();
      ws.send(JSON.stringify({ type: 'join', api: 1, cols, rows }));
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
