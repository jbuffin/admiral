# 12. Ships live in tmux, and everything else in `~/.admiral/`

Status: accepted

## Context

Ships run in tmux sessions on Admiral's own socket and outlive the Admiral server (ADR 0002). A server restart, from `admiral service` or an upgrade, must bring back every ship as it was: its name, its fleet and harness, the order it was launched from, and where it sits in open water. ADR 0010 put the config in `~/.admiral/` but left the data dir open, and `docs/architecture.md` proposed a `state.db`. The server must stay light on memory (ADR 0005), and the engineer wants to open a ship from any terminal, not only from the bridge.

## Decision

Decided in [What survives a restart and where it lives](https://github.com/jbuffin/admiral/issues/36).

- **tmux is the only record of a ship.** Each ship's tmux session holds everything about it as session options next to `@admiral-name`: `@admiral-fleet` (key), `@admiral-fleet-name`, `@admiral-fleet-color`, `@admiral-harness`, `@admiral-dir`, `@admiral-skill`, `@admiral-source` and `@admiral-order` (the order source key and the order's `id`) `@admiral-launched` (launch time) and `@admiral-title` (the order's first line, control and bidi characters stripped, shown in the bridge header). On startup Admiral lists the sessions on its socket and rebuilds every ship from them, anchored ones included, because the dead pane is still there (ADR 0008). Admiral keeps no ship table of its own.
- **A dead tmux server ends every ship.** After a reboot or a `kill-server`, open water starts empty. There is no lost-ships list and no relaunch offer.
- **An order stays hidden while a ship with its source and `id` exists**, anchored ships included. Once the ship is gone (sailed off, dismissed, or lost with tmux), the order shows again if its source still lists it. Nothing beyond the ship's tmux options records this. The freeform list deletes an order on launch, so for it the question never comes up.
- **`$ADMIRAL_HOME` (default `~/.admiral/`) is the data dir as well as the config dir:**
  - `config.json`: the engineer's (ADR 0010).
  - `state.json`: Admiral's own, safe to delete.
  - `plugins/`: plugins (ADR 0010).
  - `data/<source-key>/`: one dir per order source instance, handed to its plugin on start. The freeform list keeps `orders.json` there.
  - `tmux.sock`: Admiral's tmux socket, used with `tmux -S`.
- **`state.json` holds what a second device should see the same:** ship positions in open water, keyed by tmux session, and the last directory used on each dock (fleet key and harness). It is JSON with `"version": 1`, held in memory and written atomically (temp file and rename), debounced about a second after a drag. Admiral drops a ship's position when the ship goes, and on startup drops any position whose session no longer exists. A corrupt file or one with an unknown version is moved aside to `state.json.bad`, and Admiral starts with defaults.
- **Purely visual settings stay in each browser's `localStorage`:** the mute toggle and the collapsed panels and sidebar. The filter text is not stored (story 49).
- **`admiral ships` and `admiral attach <ship>`.** `ships` lists the ships. `attach` takes a ship's display name or tmux session name, with shell completion, prints "Ctrl-b d to detach" once, and runs `tmux -S $ADMIRAL_HOME/tmux.sock attach -t <session>`, read-only (`-r`) for an anchored ship. Plain `tmux -S ~/.admiral/tmux.sock attach -t saltpaw` works too. An attached terminal is one more tmux client: typing in it is typing on the bridge, and it follows the bridge's sizing rule (ADR 0011).

## Considered options

- **Ships recorded only in tmux** (chosen). The record cannot drift from the sessions it describes, and a restart needs nothing but tmux.
- **A ship table in a state file or SQLite**, as `docs/architecture.md` proposed. It can disagree with tmux after a crash or a hand-killed session, and it records ships that no longer exist.
- **`~/Library/Application Support/Admiral`** for data. The macOS convention, but it splits Admiral across two places and ignores `ADMIRAL_HOME`.
- **SQLite for `state.json`.** More memory and a cgo or larger dependency for a few hundred bytes that cost little to lose.
- **Positions per browser.** No server state at all, but a ship moved on the iPad would sit somewhere else on the Mac.
- **`tmux -L admiral`.** Shorter to type, but the socket lands in tmux's `/tmp` dir, shared by every Admiral instance, so a test instance with its own `ADMIRAL_HOME` would see the real ships.

## Consequences

- Deleting `state.json` loses only ship positions and last directories.
- Backing up, inspecting or removing Admiral means one directory.
- Resuming a conversation by ID is out of the MVP, so Admiral records no conversation IDs.
- A ship launched before a change to its fleet keeps its recorded fleet name and color (ADR 0010) because they are on its tmux session.
- Anyone with a shell on the Mac can attach to a ship, as with any tmux socket the user owns.

Amended 2026-10-18 (MVP build, [spec #45](https://github.com/jbuffin/admiral/issues/45)): the filter text is not stored (story 49); ships also record `@admiral-title` for the bridge header (story 102).
