import * as d from './drive.mjs';
const pane = process.argv[2];
const s = await d.attach(pane);
await d.sleep(500); s.send('\x1bOQ'); await d.sleep(1500);
console.log((await d.screen(pane)).split('\n').filter(l=>l.trim()).slice(-25).join('\n'));
s.send('\x1b'); await d.sleep(500);
s.close();
