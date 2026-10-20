# Admiral

Visual dashboard that launches coding-agent sessions in the background and lets the engineer watch and attach to them. The build has started; designs live in `designs/`.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for jbuffin/admiral. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Building Admiral

Before writing code, read in this order:

1. `GLOSSARY.md`: the words Admiral uses.
2. `designs/prototype/index.html` (open it in a browser) and `designs/README.md`: the reference for look, layout and motion.
3. [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what to build, as user stories and implementation decisions. Behavior wins over the prototype; the prototype wins on visual detail.
4. `docs/adr/`. Open questions are GitHub issues. Do not build a part that depends on one until it is resolved and recorded.

Touch must work on iPad Safari. Use the harnesses' official logos on flags; never redraw them.

Test only what is likely to break: state logic, protocol handling, and the drag and bridge flows. Don't test markup, styling or one-line glue code, and don't write a test for every small scenario. Prefer a few end-to-end checks over many unit tests. This applies to the server as well as the UI.

### More docs

Read these when the task touches the area:

- [`docs/agents/codebase.md`](docs/agents/codebase.md): the package map, API routes, ship statuses and the setup terminal. Read it before changing the server, the launcher or ships.
- [`docs/agents/testing.md`](docs/agents/testing.md): the test seams, `$ADMIRAL_HOME` isolation, the fake harness cue language and the LAN-address skips. Read it before writing or changing a test.
- [`docs/bridge-protocol.md`](docs/bridge-protocol.md): the bridge WebSocket messages. Read it before changing the bridge or `web/src/bridgeLink.ts`.
- [`docs/cli.md`](docs/cli.md): every `admiral` command and the setup answers file. Read it before changing `internal/cli/`.
- [`docs/agents/releasing.md`](docs/agents/releasing.md): the Release workflow, its one-time setup and the manual fallback. Read it before cutting a release or changing `release.yml`.
- [`docs/personal-builds.md`](docs/personal-builds.md): building with the official harness logos. Read it before touching `web/logos/` or `web/src/Logos.tsx`.

### Before you push

CI runs `make e2e-critical`: the Go e2e tests and only the `@critical` browser flows, on the iPad viewport. The CI runner's virtual GPU resets under WebKit, so the full browser matrix flakes there (#94). Your Mac is where the full matrix runs: before you push a branch that changes code or tests, run `make e2e`, and push once it passes.

Keep the `@critical` set to the flows Admiral cannot ship broken. Every minute of browser time on CI is another chance for a GPU reset to fail the run.

### Running the tests on a Mac

- Run one Playwright suite at a time on a machine. Several runs at once (each with its own WebKit workers and trace screenshots) can exhaust WindowServer's IOSurfaces and crash it, which logs you out. Local runs use 2 workers and no trace screenshots (`web/playwright.config.ts`); CI keeps the defaults.
- Admiral's tmux server outlives Admiral by design. A test that starts Admiral must stop it before `tmux -S <home>/tmux.sock kill-server`, or Admiral starts a new tmux and it leaks. In Go, register the kill-server cleanup before `t.Cleanup(stop)`; `web/scripts/serve-e2e.mjs` does this for Playwright's shared server.
- After seeding state through the API or a file (an order, a ship, a config edit), wait for the change to show in the API or the page before reading it. Reading it once right away races the source's refresh.
- Give fake secrets in tests plain values such as `fake-token-work-2`, or the `decoy-secret-<hex>` form that `.gitleaks.toml` allowlists. A key-shaped value such as `ghp_…` fails the secret scan, and clearing it from the branch takes a history rewrite.

### Order sources and settings

- The server masks secret settings, including values inside arrays, as `__admiral_unchanged__`, and restores them on save. A settings control keeps that marker intact: a list whose stored value holds the marker stays a JSON field. Decide from what the server actually masked (`maskSecrets` in `internal/sources/service.go`), not from schema keywords alone.
- A settings change or Restart reuses the source's runner (`internal/sources/supervisor.go`), so its panels' last orders and errors carry over to the new process. Adding a source, or removing and re-adding it, starts a new runner with none.
