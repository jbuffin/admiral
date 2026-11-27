import * as d from './drive.mjs';
const s = await d.attach('p1');
s.send('echo still-alive-$PID\r');
console.log((await d.waitScreen('p1', /still-alive-\d+\s*\n/, 10000)).split('\n').filter(l=>l.trim()).slice(-3).join('\n'));
s.close();
