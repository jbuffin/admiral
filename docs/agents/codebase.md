# Codebase

This file holds the package map, API routes and ship statuses, idle memory figures and the setup terminal. Read it before changing the server, the launcher, ships or the setup terminal.

## Layout

- `designs/`: the clickable prototype, screenshots, design tokens, SVG assets and source design files. Start with `designs/README.md`.
- [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what the MVP does, as user stories, implementation decisions and testing decisions.
- `docs/architecture.md`: an early proposal for the build, superseded by the spec and the ADRs.
- `docs/adr/`: decisions.
- `GLOSSARY.md`: the words Admiral uses.
- `cmd/admiral/`, `internal/`: the Go server and CLI. `internal/auth` is access control, `internal/server` is HTTP and WebSocket, `internal/home` finds `$ADMIRAL_HOME`, `internal/config` loads, validates, watches and writes `$ADMIRAL_HOME/config.json`. `internal/sessionhost` is the one tmux control-mode connection on `$ADMIRAL_HOME/tmux.sock` (it re-reads the sessions about every half second, and a global `alert-bell` hook stamps bells), `internal/launcher` creates ships (names, hook bundle in `$ADMIRAL_HOME/harness/`, command lines), `internal/state` is `$ADMIRAL_HOME/state.json` (ship positions, each dock's last directory), `internal/ships` serves `GET`/`POST /api/ships`, `PUT /api/ships/<session>/position`, `POST /api/ships/<session>/input-seen` (a browser sent input: clears needs you), `POST /api/ships/<session>/dismiss` (ends an anchored ship) and the WebSocket `ships` message. A ship's `status` is `underway`, `needs_you`, `anchored` (with `exit_code` or `exit_signal`) or `sailing_off` (exit 0, session killed after 3 s); hooks, a Codex bell and the 10 s no-hook rule decide needs you (every hook runs `admiral hook <event>`, internal and unlisted in the usage text, which reads the harness's JSON on stdin and sets `@admiral-state` on `$TMUX_PANE`; it always exits 0. With `needs_you` it also sets `@admiral-reason` in the same tmux call, a code and never payload text: `permission:<tool>`, `question`, `notification:<type>`, `stop`, or `bell` from a Codex bell; `working` unsets it, and `sessionhost` polls it into `Session.Reason`. A tool name or type is kept only if it matches `^[A-Za-z0-9_.:-]{1,64}$`, and a notification's reason is written only if none is set, so `permission:Bash` survives either order of Claude's two events. `internal/cli/hook.go` holds the event→state and event→reason rules, and Claude's `Stop` writes nothing while `background_tasks` has a running `subagent` or `teammate`, so a running `shell` task doesn't hold a ship back. Every ship also gets `ADMIRAL_SHIP` (its session name), `ADMIRAL_REPORT` (`$ADMIRAL_HOME/reports/<session>.json`) and `ADMIRAL_INBOX` (`$ADMIRAL_HOME/inbox/<session>/`), which win over a fleet's env; Admiral removes any report and inbox a reused name left, then creates `reports/` and the inbox before the harness starts. Every start links `$ADMIRAL_HOME/bin/admiral` to the running binary and puts that directory first on a ship's `PATH`), and `HiddenOrders` lists the orders ships hide. `internal/outcomes` is the outcome log: one goroutine appends a JSON line per record to `$ADMIRAL_HOME/outcomes/<YYYY-MM>.jsonl` (month of `ts`, UTC), never reads it back, queues 1024 records and drops the rest, and logs a failed write once until one succeeds. `ships.Service` feeds it: `launched` from `serveLaunch` (and only then does `Run` follow the ship), `needs_you` and `underway` from the status each pass of `Run` sees against the last (`internal/ships/outcomes.go`; a stop whose reason changes is one stop, and a ship no hook has spoken for needs you with reason `idle`), `ended` when a pane is dead without `@admiral-ended` (also at start-up, before an exit-0 ship is killed) and `dismissed` from Dismiss. `ended` carries the parsed `$ADMIRAL_REPORT` (at most 1 MiB; `null` and a log line when invalid), and once it is written the writer sets the session option `@admiral-ended` and deletes the report and inbox of a ship that was dismissed or sailed off. Admiral kills such a ship whether or not the log works (`Service.killShip`), so one killed while its `ended` fails loses that `ended` and keeps its files; a dead pane still in tmux is retried at the next start. `launcher.ShipFiles` builds the report and inbox paths for both the environment and the cleanup. The needs-you tally survives a restart in the session options `@admiral-needs-count`, `@admiral-needs-s` and `@admiral-stop-since`. A session removed with no dead pane gets no `ended`.
- `web/`: the Preact UI. `web/embed.go` embeds the built `web/dist` into the binary.
- `packaging/`: the Homebrew formula, the curl install script and the notices generator. `make release` builds the tarball.
- `e2e/`: server tests that run the real binary.

## Idle memory

One fleet, the Orders source, no ships, on an M-series Mac (macOS 27, Go 1.26, tmux 3.7c, python 3.14), resident set size from `ps -o rss`:

| process | RSS |
| --- | --- |
| `admiral` | about 16 MB |
| the freeform plugin (`python3 plugin.py`) | about 19 MB |
| Admiral's tmux server and its control client | about 4 MB and 5 MB |

Each ship adds its harness, and a pane in tmux.

## The setup terminal

Tapping the "Set up a fleet" marina or the header's fleet count opens `admiral setup` in a terminal over open water. It runs in the tmux session `_setup` on Admiral's socket (never in `ships`, never a ship) and uses the bridge's terminal, so several browsers can share it and a dropped connection rejoins. `POST /api/setup` starts it or leaves the running one alone; `GET /api/setup` says whether it runs. The bridge accepts `/ws/bridge/_setup` while the session exists. The terminal closes when the session ends, whether setup wrote the file or not, and the page then re-reads `/api/config`; a change to `config.json` alone never closes it.
