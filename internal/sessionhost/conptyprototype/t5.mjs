import * as d from './drive.mjs';
const pane = process.argv[2];
const s = await d.attach(pane);
await d.sleep(500); s.send('\r');
await d.sleep(6000);
console.log((await d.screen(pane)).split('\n').filter(l=>l.trim()).join('\n'));
s.close();
