# Admiral: proposed architecture

A starting point for the build, not a decision record. The decisions it rests on are in `docs/adr/`.

## Shape

```
 Browser (Mac, or iPad over LAN/VPN)
   Admiral web UI ──── HTTP + WebSocket ────┐
                                            │
 Mac                                        ▼
   admiral server (one local process)
     ├── config reader ──── ~/.commodore-launcher/config.json   (fleets, docks)
     ├── state reader ───── ~/.commodore-launcher/state.db      (tasks, sessions; read-only)
     ├── task sources ───── plugins: GitHub (gh), later Jira, Linear …
     ├── skill index ────── skill directories per harness and profile
     ├── session host ───── starts `commodore open <url>` headless and attaches
     │                      (tmux, see ADR 0002)
     └── terminal bridge ── PTY/tmux output ⇄ WebSocket ⇄ xterm.js in the browser
```

## Pieces

- **Server.** One small local process the engineer starts (and can run at login). It serves the UI and a WebSocket. It binds to the LAN interface only when the engineer opts in, and requires a token or a pairing step before a non-local browser can connect, because a terminal bridge is remote code execution by design.
- **Fleets and docks.** Read from the launcher's `config.json` (`profiles.<name>.harnesses`). Watch the file and push changes to the UI. Never write it; `commodore setup`, `commodore profile …` and `commodore configure` own it.
- **Tasks and sessions.** Read the launcher's `state.db` read-only, or better, call `commodore tasks list --json` / `tasks show --json` so Admiral does not depend on the schema. A session's liveness is its session lock (`~/.commodore-launcher/locks/<task>.session.lock`); `commodore` already reports it.
- **Launching.** Run `commodore open <issue-or-pr-url> --harness <harness>` for the dock's harness. The launcher finds the repository, checks the profile association, picks or resumes the session and runs the harness. Admiral must not reimplement any of that.
- **Session host.** Today the launcher runs the harness in the terminal that ran `commodore open` (launcher ADR 0022). Admiral needs to run it headless and attach later. The launcher's `docs/session-host.md` already names a tmux-based `SessionHost` as the planned way; see ADR 0002 here.
- **Terminal bridge.** Attach to the session's tmux pane (or the PTY Admiral owns), stream bytes to the browser over the WebSocket, render with xterm.js, and send keystrokes back. Resize the pane when the bridge resizes.
- **Crew.** Teammates and subagents need a source: Commodore's own reporting, the harness's transcript, or tmux panes it opens. Unknown today; see `docs/open-questions.md`.
- **Needs you.** Needs a signal per session; see `docs/open-questions.md`.
- **Task sources.** A plugin interface returning orders `{id, title, url, source, labels, priority}` and a way to refresh. GitHub first, through `gh` (already a launcher requirement). A launched order must map to a GitHub issue or PR URL while the launcher accepts only those.
- **UI.** Any component framework. The prototype's markup, CSS and motion are the reference; port its look, not its code. Keep a single page; the bridge and launch orders are overlays.

## Data the UI needs

| Thing | Fields |
| --- | --- |
| Fleet | `profile`, `name`, `color`, `docks[]` |
| Dock | `fleet`, `harness`, `logo`, `shipsAtSea` |
| Order | `id`, `title`, `url`, `source`, `hot`, `panel` |
| Ship | `task`, `session`, `fleet`, `harness`, `skill`, `name`, `status` (`underway`, `needs_you`, `docked`), `position?`, `crew[]` |
| Crew boat | `id`, `name`, `role` (teammate, subagent), `job` |
| Terminal stream | per ship and per crew boat: bytes out, keystrokes in, size |

## Build order

1. Server skeleton: serve the static UI, read fleets from `config.json`, list tasks and sessions.
2. Static harbor screen matching the prototype, with real fleets and docks and fake ships.
3. Orders from GitHub; drag and tap to a dock; launch orders; `commodore open` through the headless host.
4. Ships from real sessions; liveness from session locks; ships leave and return to docks.
5. Bridge: live terminal over WebSocket, typing and keys.
6. Needs you, crew boats, sound, persistence of layout, polish.
