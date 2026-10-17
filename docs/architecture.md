# Admiral: proposed architecture

A starting point for the build, not a decision record. The decisions it rests on are in `docs/adr/`. Admiral is a standalone session launcher (ADR 0003); it does not use Commodore Launcher.

## Shape

```
 Browser (Mac, or iPad over LAN/VPN)
   Admiral web UI ──── HTTP + WebSocket ────┐
                                            │
 Mac                                        ▼
   admiral server (one local process)
     ├── fleet config ───── ~/.admiral/config.json   (fleets, search roots, favorite skills, sources; ADR 0010)
     ├── state ──────────── ~/.admiral/state.json    (ship positions, last directory per dock; ADR 0012)
     ├── task sources ───── plugins: GitHub (gh), later Jira, Linear …
     ├── repo finder ────── fleet search roots → checkouts
     ├── skill index ────── skill directories per harness and fleet
     ├── session host ───── one tmux session per ship on Admiral's own socket (ADR 0002)
     └── terminal bridge ── tmux pane ⇄ WebSocket ⇄ xterm.js in the browser
```

`~/.admiral/` holds the config (ADR 0010), `state.json`, each order source's `data/<source-key>/` and the tmux socket `tmux.sock` (ADR 0012).

## Pieces

- **Server.** One small local process the engineer starts (and can run at login). It serves the UI and a WebSocket. It binds to the LAN interface only when the engineer opts in, and requires a token or a pairing step before a non-local browser can connect, because a terminal bridge is remote code execution by design (ADR 0001).
- **Fleets and docks.** Admiral's own config: each fleet has a name, an optional color, and its harnesses, each with an executable, extra arguments and environment (for example `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`), and its search roots. `admiral setup` and `admiral fleet` write it, as Commodore Launcher's setup and `profile` commands do. Watch the file and push changes to the UI (ADR 0010).
- **Repositories.** A ship runs in a directory the engineer picks in launch orders from the git checkouts under the fleet's search roots, pre-selected from the order when its URL matches a checkout's remote. No typed paths, no cloning, no worktrees of Admiral's own (ADR 0009).
- **Launching.** Start the dock's harness in a new tmux session as an argument list: the fleet's executable and arguments, Admiral's hook flags, then the prompt (`claude -- '/skill text'`, `codex -- '$skill text'`, `copilot --interactive='/skill text'`), with the fleet's environment and the checkout as working directory. Hooks are `tmux set -p @admiral-state <state>`, from a bundle in `~/.admiral/harness/`; Admiral skips no trust dialog (ADR 0013). Commodore is the default skill.
- **Sessions.** Each ship's tmux session is its only record: name, fleet, harness, directory, skill, order source and order `id` as `@admiral-*` session options. A restart rebuilds the ships from tmux (ADR 0012). `admiral ships` and `admiral attach <ship>` open a ship from any terminal. Resume is out of the MVP.
- **Terminal bridge.** Attach to the ship's tmux pane, stream bytes to the browser over the WebSocket, render with xterm.js, and send keystrokes back. One tmux control client per watched ship, shared by every browser; the latest active browser sets the pane size, a joining browser gets the screen and 2,000 lines of scrollback, and a bridge reconnects after a server restart without queueing input (ADR 0011).
- **Skill index.** List the skills installed for a harness and fleet, plus the chosen directory's project skills, for the type-ahead in launch orders. Read the folders with the fleet's environment each time launch orders opens; no cache. Claude Code: `<CLAUDE_CONFIG_DIR>/skills`, the project's `.claude/skills`, and enabled plugins. Codex CLI: `~/.agents/skills`, `$CODEX_HOME/skills`, the project's `.agents/skills`. Copilot CLI: `~/.copilot/skills` (or `$COPILOT_HOME/skills`), `~/.agents/skills`, the project's `.github/skills`, `.agents/skills` and `.claude/skills`.
- **Crew.** Teammates and subagents need a source: Commodore's own reporting, the harness's transcript, or tmux panes it opens. Unknown today; see `docs/open-questions.md`.
- **Needs you.** Needs a signal per session; see `docs/open-questions.md`.
- **Task sources.** A plugin interface returning orders `{id, title, url, source, labels, priority}` and a way to refresh. GitHub first, through `gh`.
- **UI.** Preact, TypeScript and Vite (ADR 0004). The prototype's markup, CSS and motion are the reference; port its look, not its code. Keep a single page; the bridge and launch orders are overlays.

## Data the UI needs

| Thing | Fields |
| --- | --- |
| Fleet | `id`, `name`, `color`, `docks[]` |
| Dock | `fleet`, `harness`, `logo`, `shipsAtSea` |
| Order | `id`, `title`, `url`, `source`, `hot`, `panel` |
| Ship | `id`, `order`, `fleet`, `harness`, `skill`, `name`, `status` (`underway`, `needs_you`, `anchored`), `position?`, `crew[]` |
| Crew boat | `id`, `name`, `role` (teammate, subagent), `job` |
| Terminal stream | per ship and per crew boat: bytes out, keystrokes in, size |

## Build order

1. Server skeleton: serve the static UI, read fleets from Admiral's config.
2. Static harbor screen matching the prototype, with real fleets and docks and fake ships.
3. Session host: start a harness in tmux under a fleet's settings in a checkout, and list running ships after a restart.
4. Orders from GitHub; drag and tap to a dock; launch orders; ships leave and return to docks.
5. Bridge: live terminal over WebSocket, typing and keys.
6. Needs you, crew boats, sound, persistence of layout, polish.
