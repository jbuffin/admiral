# Admiral: proposed architecture

A starting point for the build, not a decision record. The decisions it rests on are in `docs/adr/`. Admiral is a standalone session launcher (ADR 0003); it does not use Commodore Launcher.

## Shape

```
 Browser (Mac, or iPad over LAN/VPN)
   Admiral web UI ──── HTTP + WebSocket ────┐
                                            │
 Mac                                        ▼
   admiral server (one local process)
     ├── fleet config ───── ~/.admiral/config.json   (fleets, docks, harness settings, skill list)
     ├── state ──────────── ~/.admiral/state.db      (ships, names, positions, conversation IDs)
     ├── task sources ───── plugins: GitHub (gh), later Jira, Linear …
     ├── repo finder ────── order → local checkout
     ├── skill index ────── skill directories per harness and fleet
     ├── session host ───── one tmux session per ship on Admiral's own socket (ADR 0002)
     └── terminal bridge ── tmux pane ⇄ WebSocket ⇄ xterm.js in the browser
```

The paths under `~/.admiral/` are proposals.

## Pieces

- **Server.** One small local process the engineer starts (and can run at login). It serves the UI and a WebSocket. It binds to the LAN interface only when the engineer opts in, and requires a token or a pairing step before a non-local browser can connect, because a terminal bridge is remote code execution by design (ADR 0001).
- **Fleets and docks.** Admiral's own config: each fleet has a name, an optional color, and its harnesses, each with an executable, extra arguments and environment (for example `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`). Watch the file and push changes to the UI. How the engineer edits it is open; see `docs/open-questions.md`.
- **Repositories.** An order needs a local checkout to run in. For GitHub orders the repository is in the URL; Admiral finds its checkout on disk. Where it looks, whether it clones, and whether each ship gets its own worktree are open questions.
- **Launching.** Start the dock's harness in a new tmux session: the fleet's executable, arguments and environment, the checkout as working directory, and the chosen skill with the order's URL as the launch prompt. Each harness names a skill differently (`/name <url>` for Claude Code, `--interactive='/name <url>'` for Copilot CLI, `$name <url>` for Codex CLI); Commodore Launcher's `docs/harnesses.md` records how each was verified. Commodore is the default skill.
- **Sessions and resume.** Record each ship's task, fleet, harness, skill, tmux session and, where the harness has one, its conversation ID. Claude Code and Copilot CLI can resume a conversation by ID; Codex CLI cannot.
- **Terminal bridge.** Attach to the ship's tmux pane, stream bytes to the browser over the WebSocket, render with xterm.js, and send keystrokes back. Resize the pane when the bridge resizes.
- **Skill index.** List the skills installed for a harness and fleet, for the type-ahead in launch orders. Claude Code: `<CLAUDE_CONFIG_DIR>/skills`, the project's `.claude/skills`, and enabled plugins. Codex CLI: `~/.agents/skills`, `$CODEX_HOME/skills`, the project's `.agents/skills`. Copilot CLI: `~/.copilot/skills` (or `$COPILOT_HOME/skills`), `~/.agents/skills`, the project's `.github/skills`, `.agents/skills` and `.claude/skills`.
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
| Ship | `id`, `order`, `fleet`, `harness`, `skill`, `name`, `status` (`underway`, `needs_you`, `docked`), `position?`, `crew[]` |
| Crew boat | `id`, `name`, `role` (teammate, subagent), `job` |
| Terminal stream | per ship and per crew boat: bytes out, keystrokes in, size |

## Build order

1. Server skeleton: serve the static UI, read fleets from Admiral's config.
2. Static harbor screen matching the prototype, with real fleets and docks and fake ships.
3. Session host: start a harness in tmux under a fleet's settings in a checkout, and list running ships after a restart.
4. Orders from GitHub; drag and tap to a dock; launch orders; ships leave and return to docks.
5. Bridge: live terminal over WebSocket, typing and keys.
6. Needs you, crew boats, sound, persistence of layout, polish.
