import * as d from './drive.mjs';
const p = await d.spawn('pwsh', 120, 36);
const s = await d.attach(p.pane);
await d.waitScreen(p.pane, /PS .*>/, 20000);
s.send('1..200 | % { "line-$_" }\r');
await d.waitScreen(p.pane, /line-200[\s\S]*PS .*>/, 20000);
const count = async () => { const dmp = await d.dump(p.pane); const all = dmp.split('\n').filter(l=>/^[SV]/.test(l)).map(l=>l.slice(7).trim()); const c={}; for (const l of all) if (/^line-\d+$/.test(l)) c[l]=(c[l]||0)+1; return { sb: dmp.split('\n')[0], uniq: Object.keys(c).length, dups: Object.entries(c).filter(([,n])=>n>1).map(([k,n])=>k+'×'+n) }; };
console.log('before', JSON.stringify(await count()));
for (const [c, r] of [[80, 24], [140, 40], [60, 30], [120, 36]]) { s.resize(c, r); await d.sleep(800); console.log(`after ${c}x${r}`, JSON.stringify(await count()).slice(0, 400)); }
for (let i = 0; i < 12; i++) { s.resize(i % 2 ? 80 : 140, i % 2 ? 24 : 40); await d.sleep(100); }
s.resize(120, 36); await d.sleep(1500);
console.log('after storm', JSON.stringify(await count()).slice(0, 600));
console.log(p.pane);
s.close();
