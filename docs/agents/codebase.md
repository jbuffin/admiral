# Codebase

The package map, API routes and ship statuses, idle memory figures and the setup terminal. Read this before changing the server, the launcher, ships or the setup terminal.

## Layout

- `designs/`: the clickable prototype, screenshots, design tokens, SVG assets and source design files. Start with `designs/README.md`.
- [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what the MVP does, as user stories, implementation decisions and testing decisions.
- `docs/architecture.md`: an early proposal for the build, superseded by the spec and the ADRs.
- `docs/adr/`: decisions.
- `GLOSSARY.md`: the words Admiral uses.
- `cmd/admiral/`, `internal/`: the Go server and CLI. `internal/auth` is access control, `internal/server` is HTTP and WebSocket, `internal/home` finds `$ADMIRAL_HOME`, `internal/config` loads, validates, watches and writes `$ADMIRAL_HOME/config.json`. `internal/sessionhost` is the one tmux control-mode connection on `$ADMIRAL_HOME/tmux.sock` (it re-reads the sessions about every half second, and a global `alert-bell` hook stamps bells), `internal/launcher` creates ships (names, hook bundle in `$ADMIRAL_HOME/harness/`, command lines), `internal/state` is `$ADMIRAL_HOME/state.json` (ship positions, each dock's last directory), `internal/ships` serves `GET`/`POST /api/ships`, `PUT /api/ships/<session>/position`, `POST /api/ships/<session>/input-seen` (a browser sent input: clears needs you), `POST /api/ships/<session>/dismiss` (ends an anchored ship) and the WebSocket `ships` message. A ship's `status` is `underway`, `needs_you`, `anchored` (with `exit_code` or `exit_signal`) or `sailing_off` (exit 0, session killed after 3 s); hooks, a Codex bell and the 10 s no-hook rule decide needs you, and `HiddenOrders` lists the orders ships hide.
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
