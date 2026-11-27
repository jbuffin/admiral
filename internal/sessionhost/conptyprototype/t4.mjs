import * as d from './drive.mjs';
const pane = process.argv[2];
const s = await d.attach(pane);
await d.sleep(2000);
const box = async () => { const l = (await d.screen(pane)).split('\n'); const i = l.findLastIndex(x => /^─{20}/.test(x)); const j = l.slice(0, i).findLastIndex(x => /^─{20}/.test(x)); return l.slice(j + 1, i).join(' | '); };
for (const [name, seq] of [['kitty CSI 13;2u', '\x1b[13;2u'], ['modifyOtherKeys 27;2;13~', '\x1b[27;2;13~'], ['ESC CR (meta-enter)', '\x1b\r'], ['plain LF', '\n']]) {
  s.send('one'); await d.sleep(200); s.send(seq); await d.sleep(200); s.send('two'); await d.sleep(700);
  console.log(`${name}: box = ${JSON.stringify(await box())}`);
  s.send('\x15'); await d.sleep(100); s.send('\x1b[F'); for (let i=0;i<12;i++) s.send('\x7f'); s.send('\x15'); await d.sleep(500);
}
console.log('after clear:', JSON.stringify(await box()));
s.close();
