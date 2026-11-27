// PROTOTYPE driver for #876: talks to the bridge stub like a browser would, so
// the measurements can run without clicking. node drive.mjs <scenario> [args]
const BASE = 'http://127.0.0.1:7876';
const ESC = '\x1b';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function spawn(preset, cols = 120, rows = 36, answer = 'host') {
  const r = await fetch(`${BASE}/api/spawn?preset=${preset}&cols=${cols}&rows=${rows}&answer=${answer}`, { method: 'POST' });
  return r.json();
}
export async function dump(pane) { return (await fetch(`${BASE}/api/dump?pane=${pane}`)).text(); }
export async function screen(pane) {
  return (await dump(pane)).split('\n').filter((l) => l.startsWith('V')).map((l) => l.slice(7)).join('\n');
}
export async function hooks() { return (await fetch(`${BASE}/api/hooks`)).text(); }
export async function panes() { return (await fetch(`${BASE}/api/panes`)).json(); }

export function attach(pane, { cols = 120, rows = 36, snap = 'render' } = {}) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:7876/ws/bridge/${pane}?snap=${snap}`);
    ws.binaryType = 'arraybuffer';
    const s = { ws, events: [], join: null, bytes: 0, out: [] };
    s.send = (data) => ws.send(JSON.stringify({ type: 'input', data }));
    s.resize = (c, r) => ws.send(JSON.stringify({ type: 'resize', cols: c, rows: r }));
    s.close = () => ws.close();
    s.text = () => Buffer.concat(s.out).toString('utf8');
    ws.onopen = () => ws.send(JSON.stringify({ type: 'join', api: 1, cols, rows }));
    ws.onerror = reject;
    ws.onmessage = (e) => {
      if (typeof e.data !== 'string') { s.bytes += e.data.byteLength; s.out.push(Buffer.from(e.data)); return; }
      const m = JSON.parse(e.data);
      if (m.type === 'join') { s.join = m; resolve(s); }
      else if (m.type === 'event') s.events.push(m.event);
    };
  });
}

export async function waitScreen(pane, re, ms = 30000) {
  const end = Date.now() + ms;
  let last = '';
  while (Date.now() < end) {
    last = await screen(pane);
    if (re.test(last)) return last;
    await sleep(300);
  }
  throw new Error(`timeout waiting for ${re}\n--- screen ---\n${last}`);
}

export { sleep, ESC };

if (import.meta.url === `file:///${process.argv[1].replace(/\\/g, '/')}` || process.argv[1].endsWith('drive.mjs')) {
  const [cmd, ...args] = process.argv.slice(2);
  if (cmd === 'screen') console.log(await screen(args[0]));
  else if (cmd === 'dump') console.log(await dump(args[0]));
  else if (cmd === 'hooks') console.log(await hooks());
  else if (cmd === 'panes') console.log(JSON.stringify(await panes(), null, 1));
}
