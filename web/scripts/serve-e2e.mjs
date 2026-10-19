// Runs the admiral binary for Playwright's webServer and, once it has stopped,
// ends the tmux server it started (that server outlives Admiral).
// Usage: node serve-e2e.mjs <admiral> <listen>
import { spawn, spawnSync } from 'node:child_process';
import { join } from 'node:path';

const [bin, listen] = process.argv.slice(2);
const child = spawn(bin, ['--listen', listen], { stdio: 'inherit' });
for (const sig of ['SIGTERM', 'SIGINT']) process.on(sig, () => child.kill('SIGTERM'));

child.on('error', (err) => {
  console.error(`could not start ${bin}: ${err.message}`);
  process.exit(1);
});

child.on('exit', (code) => {
  const sock = join(process.env.ADMIRAL_HOME, 'tmux.sock');
  spawnSync('tmux', ['-S', sock, 'kill-server']);
  for (let i = 0; i < 20 && spawnSync('tmux', ['-S', sock, 'list-sessions']).status === 0; i++) {
    spawnSync('sleep', ['0.1']);
  }
  process.exit(code ?? 1);
});
