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

The server tests that connect through the Mac's own LAN address skip, with a message, when that address doesn't answer. They always run in CI.

For UI work, run `bin/admiral --listen 127.0.0.1:7420` and then `npm --prefix web run dev`. Vite forwards `/api` and `/ws` to the server (set `ADMIRAL_SERVER` for another address).
