# Admiral

A visual dashboard for launching and watching coding-agent sessions (Claude Code, Codex CLI, GitHub Copilot CLI): your work queues on the left, a cartoon ocean of running sessions in the middle, and one marina per fleet on the right. Drag a task onto a harness's dock to start a session in the background, on Commodore or another skill; tap a ship to attach to its terminal.

The designs are done and the MVP build has started.

![Admiral harbor](designs/screenshots/harbor-1440x900.png)

## Layout

- `designs/`: the clickable prototype, screenshots, design tokens, SVG assets and source design files. Start with `designs/README.md`.
- [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what the MVP does, as user stories, implementation decisions and testing decisions.
- `docs/architecture.md`: an early proposal for the build, superseded by the spec and the ADRs.
- `docs/adr/`: decisions.
- `GLOSSARY.md`: the words Admiral uses.
- `cmd/admiral/`, `internal/`: the Go server and CLI. `internal/auth` is access control, `internal/server` is HTTP and WebSocket, `internal/home` finds `$ADMIRAL_HOME`, `internal/config` loads, validates, watches and writes `$ADMIRAL_HOME/config.json`.
- `web/`: the Preact UI. `web/embed.go` embeds the built `web/dist` into the binary.
- `e2e/`: server tests that run the real binary.

## Development

You need Go (version in `go.mod`), Node (version in `.nvmrc`) and tmux.

```sh
npm --prefix web ci                 # once per checkout
npm --prefix web exec playwright install webkit   # once, for the browser tests
make build                          # UI, then bin/admiral
bin/admiral --listen 127.0.0.1:7420 # prints the local URL and the URL for other devices
make lint                           # go vet and the TypeScript check
make test                           # Go and Vitest unit tests
make e2e                            # builds, then the server and browser tests
```

Admiral keeps its data in `$ADMIRAL_HOME` (default `~/.admiral/`). Every test starts the binary with a fresh temporary one on a free port, so tests never touch yours.

Tests sit at two seams. `e2e/` (Go) starts `bin/admiral` and drives it over HTTP and WebSocket. `web/e2e/` (Playwright, WebKit) loads the page at iPad landscape, iPad portrait and desktop sizes. Vitest covers pure UI state logic only.

Tests that launch ships use the fake harnesses in `e2e/fakeharness/` (`claude`, `codex` and `copilot` are symlinks to `fake.sh`). A test puts one as the `executable` of a fleet and passes two variables in the fleet's `env`: `FAKE_RECORD_DIR` (where to record) and `FAKE_CUE` (an optional cue file). Started in a ship's pane, the fake writes `<FAKE_RECORD_DIR>/<tmux session>/` with `harness`, `argv` (each argument followed by a NUL byte), `env` (one `NAME=value` per line), `cwd`, `name` (the `@admiral-name` option as read at start), `options` (all session options at start) and last an empty `recorded` file to wait for. Then it runs the cue file, one cue per line: `hook <needs_you|working>` runs `tmux set -p @admiral-state <state>`, `bell` rings the bell, `sleep <n>` waits n seconds, `exit <n>` exits with status n. It creates `cues-done` when the cues end, then stays running unless a cue exited. The same protocol is in the header of `fake.sh`. Tests that run tmux set `TMUX_TMPDIR` to a temporary directory and talk to tmux only with `tmux -S <home>/tmux.sock`.

The server tests that connect through the Mac's own LAN address skip, with a message, when that address doesn't answer. They always run in CI.

For UI work, run `bin/admiral --listen 127.0.0.1:7420` and then `npm --prefix web run dev`. Vite forwards `/api` and `/ws` to the server (set `ADMIRAL_SERVER` for another address).

### Logos

The flags draw a logo `<symbol>` by id (`logo-claude`, `logo-copilot`, `logo-codex`). Without official files the build uses lettered placeholders (Cl, Co, Cx). For a personal build, put the official logo files in `web/logos/official/` as `claude.svg`, `copilot.svg` and `codex.svg` (the folder is gitignored) and rebuild; they replace the placeholders under the same ids. Each file holds the mark only, on a transparent background, with a `viewBox`. Admiral draws each harness's field from the vendors' usage rules (Claude ivory, Copilot dark, Codex white) and centres the mark with clear space. Never redraw a vendor logo or commit these files.
