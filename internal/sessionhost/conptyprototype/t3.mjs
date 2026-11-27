import * as d from './drive.mjs';
const [pane, who] = process.argv.slice(2);
const s = await d.attach(pane);
await d.sleep(4000);
const hookCount = async () => (await d.hooks()).trim().split('\n').filter(Boolean).length;
async function waitStop(before, ms = 90000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    const h = (await d.hooks()).trim().split('\n').filter(Boolean).slice(before).map(JSON.parse);
    if (h.some(x => /Stop|notify/.test(x.event + x.who))) return h;
    await d.sleep(500);
  }
  return (await d.hooks()).trim().split('\n').filter(Boolean).slice(before).map(JSON.parse);
}
const tests = [
  ['typed', async (t) => { s.send(t); await d.sleep(400); s.send('\r'); }, 'Reply with exactly the word ALPHA.'],
  ['bracketed', async (t) => { s.send('\x1b[200~' + t + '\x1b[201~'); await d.sleep(400); s.send('\r'); }, 'Reply with exactly the word BRAVO.'],
  ['raw-one-write', async (t) => { s.send(t + '\r'); }, 'Reply with exactly the word CHARLIE.'],
  ['bracketed-multiline', async (t) => { s.send('\x1b[200~' + t + '\x1b[201~'); await d.sleep(600); s.send('\r'); }, 'First line says nothing.\nSecond line: reply with exactly the words DELTA ECHO.'],
];
for (const [name, fn, text] of tests) {
  const before = await hookCount();
  await fn(text);
  await d.sleep(1500);
  const draft = await d.screen(pane);
  const h = await waitStop(before);
  const scr = await d.screen(pane);
  const word = text.match(/[A-Z]{4,}( [A-Z]{4,})?\.$/)[0].replace('.', '');
  const submits = h.filter(x => /UserPromptSubmit/.test(x.event));
  console.log(`\n=== ${name}: submits=${submits.length} stop=${h.some(x=>/Stop|notify/.test(x.event+x.who))} answered=${scr.includes(word)}`);
  console.log('hooks:', JSON.stringify(h.map(x => ({ who: x.who, ev: x.event, pane: x.ADMIRAL_PANE, prompt: (x.stdin.match(/"prompt":"([^"]*)/)||[])[1] }))));
  console.log('--- screen after 1.5s ---\n' + draft.split('\n').filter(l => l.trim()).slice(-12).join('\n'));
  await d.sleep(1000);
}
console.log('\n--- final screen ---\n' + (await d.screen(pane)).split('\n').filter(l=>l.trim()).join('\n'));
s.close();
