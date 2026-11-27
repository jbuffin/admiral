// PROTOTYPE hook probe for #876: records which environment a harness hook gets.
// usage: node probe.js <who> <event> [json-arg]
const fs = require('fs');
const path = require('path');
const out = path.join(process.env.LOCALAPPDATA || '.', 'admiral-proto-876-WIPE-ME', 'hooks.jsonl');
const [who, event, arg] = process.argv.slice(2);
let stdin = '';
const done = () => {
  const rec = {
    t: new Date().toISOString(), who, event,
    ADMIRAL_PANE: process.env.ADMIRAL_PANE ?? null,
    TMUX_PANE: process.env.TMUX_PANE ?? null,
    cwd: process.cwd(),
    shell_hint: process.env.SHELL || process.env.ComSpec || null,
    pwsh: !!process.env.PSModulePath && !process.env.SHELL,
    env_count: Object.keys(process.env).length,
    argv_json: arg ? String(arg).slice(0, 200) : undefined,
    stdin: stdin.slice(0, 300),
  };
  fs.appendFileSync(out, JSON.stringify(rec) + '\n');
  process.exit(0);
};
if (process.stdin.isTTY) done();
else {
  process.stdin.on('data', (d) => (stdin += d));
  process.stdin.on('end', done);
  setTimeout(done, 1500);
}
