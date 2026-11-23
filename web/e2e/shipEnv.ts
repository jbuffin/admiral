// Variables an Admiral ship, a tmux pane or an engineer's shell sets that must not reach the bin/admiral a test starts.
// The twin is world.baseEnv in e2e/ships_helpers_test.go, which also drops HOME and XDG_CONFIG_HOME and points them at a
// temp dir; here they stay, because a test that wants a different home passes it (seedFleets' userHome).
const LEAKED = new Set([
  'ADMIRAL_HOME',
  'ADMIRAL_SHIP',
  'ADMIRAL_REPORT',
  'ADMIRAL_INBOX',
  'TMUX',
  'TMUX_PANE',
  'TMUX_TMPDIR',
  'CLAUDE_CONFIG_DIR',
  'CODEX_HOME',
  'COPILOT_HOME',
  'CLAUDE_CODE_PLUGIN_CACHE_DIR',
  'CLAUDE_CODE_AUTO_COMPACT_WINDOW',
  'ADMIRAL_AUTOPILOT',
  'ADMIRAL_LOW_STAKES',
  'ADMIRAL_AUTO_MERGE',
]);

/** process.env without the variables of the ship or shell the tests run in. */
export function cleanEnv(): NodeJS.ProcessEnv {
  return Object.fromEntries(Object.entries(process.env).filter(([k]) => !LEAKED.has(k)));
}
