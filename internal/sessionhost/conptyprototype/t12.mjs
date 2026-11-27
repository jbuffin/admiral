import * as d from './drive.mjs';
const p = await d.spawn('codex');
await d.sleep(500);
const s = await d.attach(p.pane);
await d.waitScreen(p.pane, /Ask Codex/, 30000); await d.sleep(1000);
async function turn(label, pre) {
  const n0 = s.events.length;
  if (pre) { s.send(pre); await d.sleep(200); }
  s.send('Reply with exactly the word ' + label + '. Do not run any commands.'); await d.sleep(400); s.send('\r');
  await d.waitScreen(p.pane, new RegExp('• ' + label), 90000); await d.sleep(2500);
  const ev = s.events.slice(n0);
  console.log(label, 'focus-sent:', JSON.stringify(pre || 'none'), 'bells:', ev.filter(e => e.kind === 'bell').length);
}
await turn('ONE', null);          // never told anything about focus
await turn('TWO', '\x1b[O');      // focus out
await turn('THREE', '\x1b[I');    // focus in again
s.close();
console.log(p.pane);
