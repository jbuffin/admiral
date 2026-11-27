import * as d from './drive.mjs';
const p = await d.spawn('codex');
const pane = p.pane; console.log('spawned', p);
const s = await d.attach(pane);
await d.waitScreen(pane, /Ask Codex|›/, 30000);
await d.sleep(1500);
const hookLines = async () => (await d.hooks()).trim().split('\n').filter(Boolean);
async function waitStop(before, ms = 120000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    const h = (await hookLines()).slice(before).map(JSON.parse);
    if (h.some(x => /Stop/.test(x.event) || x.who === 'codex-notify')) { await d.sleep(1500); return (await hookLines()).slice(before).map(JSON.parse); }
    await d.sleep(500);
  }
  return (await hookLines()).slice(before).map(JSON.parse);
}
const tests = [
  ['typed', async (t) => { s.send(t); await d.sleep(400); s.send('\r'); }, 'Reply with exactly the word ALPHA. Do not run any commands.'],
  ['bracketed', async (t) => { s.send('\x1b[200~' + t + '\x1b[201~'); await d.sleep(400); s.send('\r'); }, 'Reply with exactly the word BRAVO. Do not run any commands.'],
  ['raw-one-write', async (t) => { s.send(t + '\r'); }, 'Reply with exactly the word CHARLIE. Do not run any commands.'],
  ['bracketed-multiline', async (t) => { s.send('\x1b[200~' + t + '\x1b[201~'); await d.sleep(600); s.send('\r'); }, 'First line says nothing.\nSecond line: reply with exactly the words DELTA ECHO. Do not run any commands.'],
  ['raw-multiline-one-write', async (t) => { s.send(t + '\r'); }, 'First line says nothing.\nSecond line: reply with exactly the words FOX GOLF. Do not run any commands.'],
];
for (const [name, fn, text] of tests) {
  const before = (await hookLines()).length;
  const ev0 = s.events.length;
  s.send('\x1b[O'); // focus out, as when the engineer is looking elsewhere
  await d.sleep(200);
  await fn(text);
  await d.sleep(1500);
  const draft = await d.screen(pane);
  const h = await waitStop(before);
  const scr = await d.screen(pane);
  const word = text.match(/words? ([A-Z]{3,}( [A-Z]{3,})?)\./)[1];
  const evs = s.events.slice(ev0);
  console.log(`\n=== ${name}: submits=${h.filter(x => /UserPromptSubmit/.test(x.event)).length} answered=${scr.includes('• ' + word) || scr.split('\n').some(l => l.trim().endsWith(word) && !l.includes('Reply'))}`);
  console.log('hooks:', JSON.stringify(h.map(x => ({ who: x.who, ev: x.event, pane: x.ADMIRAL_PANE }))));
  console.log('bells:', evs.filter(e => e.kind === 'bell').map(e => e.t), 'titles:', JSON.stringify(evs.filter(e => e.kind === 'title').map(e => e.title).slice(-4)), 'notify/osc:', JSON.stringify(evs.filter(e => e.kind === 'notify' || (e.kind==='seq' && /notify/.test(e.seq))).map(e=>e.raw||e.body)));
  console.log('--- draft (1.5s after send) ---\n' + draft.split('\n').filter(l => l.trim()).slice(-8).join('\n'));
  s.send('\x1b[I');
  await d.sleep(1000);
}
console.log('\n--- final ---\n' + (await d.screen(pane)).split('\n').filter(l=>l.trim()).slice(-40).join('\n'));
s.close();
