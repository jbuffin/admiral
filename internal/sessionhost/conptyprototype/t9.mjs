import * as d from './drive.mjs';
const pane = process.argv[2];
const s = await d.attach(pane);
await d.sleep(1000);
const before = await d.screen(pane);
for (let i = 0; i < 12; i++) { s.resize(i % 3 === 0 ? 80 : i % 3 === 1 ? 140 : 60, i % 2 ? 24 : 40); await d.sleep(120); }
s.resize(120, 36); await d.sleep(2500);
const after = await d.screen(pane);
const dump = await d.dump(pane);
console.log(dump.split('\n')[0]);
console.log('--- after storm, back at 120x36 ---\n' + after.split('\n').filter(l=>l.trim()).join('\n'));
const sb = dump.split('\n').filter(l=>l.startsWith('S')).map(l=>l.slice(7));
const counts = {}; for (const l of sb) if (l.trim().length > 8) counts[l] = (counts[l]||0)+1;
console.log('scrollback lines', sb.length, 'repeated non-trivial lines:', Object.entries(counts).filter(([,n])=>n>1).length);
s.close();
