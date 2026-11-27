import * as d from './drive.mjs';
const pane = process.argv[2];
for (const snap of ['render', 'replay']) {
  const s = await d.attach(pane, { snap });
  const j = s.join;
  console.log(`snap=${snap} size=${JSON.stringify(j.size)} modes=${JSON.stringify(j.modes)} cursor=${JSON.stringify(j.cursor)} screenLines=${j.screen.split('\n').length} scrollback=${j.scrollback ? j.scrollback.split('\n').length : 0} replayBytes=${j.replay ? Buffer.from(j.replay,'base64').length : 0}`);
  s.close();
}
