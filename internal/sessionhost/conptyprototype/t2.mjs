import * as d from './drive.mjs';
const pane = process.argv[2];
const s = await d.attach(pane);
s.send('\x1b[B'); await d.sleep(300); s.send('\r');
const scr = await d.waitScreen(pane, /❯|>\s*$|Try "/m, 30000).catch(e => e.message);
console.log(scr);
s.close();
