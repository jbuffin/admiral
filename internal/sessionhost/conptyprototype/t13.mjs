import * as d from './drive.mjs';
const p = await d.spawn('codex');
await d.sleep(3000); // host sends focus-out at 2 s with no browser
let s = await d.attach(p.pane);
await d.waitScreen(p.pane, /Ask Codex/, 30000); await d.sleep(800);
s.send('Reply with exactly the word KILO. Do not run any commands.'); await d.sleep(400); s.send('\r');
await d.sleep(800); s.close(); // the driver leaves; nobody is watching
await d.waitScreen(p.pane, /• KILO/, 90000); await d.sleep(2500);
const fs = await import('fs');
const dir = process.env.LOCALAPPDATA + '/admiral-proto-876-WIPE-ME';
const f = fs.readdirSync(dir).filter(n => n.includes(`-${p.pane}-codex.events`)).sort().pop();
const ev = fs.readFileSync(dir + '/' + f, 'utf8').trim().split('\n').map(JSON.parse);
console.log(p.pane, 'focus-out:', JSON.stringify(ev.filter(e => e.kind === 'focus-out').map(e => [e.t, e.why])), 'bells:', ev.filter(e => e.kind === 'bell').map(e => e.t));
