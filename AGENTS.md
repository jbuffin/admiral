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

Admiral launches sessions itself (ADR 0003): it never calls `commodore` and never reads or writes `~/.commodore-launcher/`. Touch must work on iPad Safari. Use the harnesses' official logos on flags; never redraw them.

Test only what is likely to break: state logic, protocol handling, and the drag and bridge flows. Don't test markup, styling or one-line glue code, and don't write a test for every small scenario. Prefer a few end-to-end checks over many unit tests. This applies to the server as well as the UI.

### Running the tests on a Mac

- Run one Playwright suite at a time on a machine. Several runs at once (each with its own WebKit workers and trace screenshots) can exhaust WindowServer's IOSurfaces and crash it, which logs you out. Local runs use 2 workers and no trace screenshots (`web/playwright.config.ts`); CI keeps the defaults.
- Admiral's tmux server outlives Admiral by design. A test that starts Admiral must stop it before `tmux -S <home>/tmux.sock kill-server`, or Admiral starts a new tmux and it leaks. In Go, register the kill-server cleanup before `t.Cleanup(stop)`; `web/scripts/serve-e2e.mjs` does this for Playwright's shared server.
- After seeding state through the API or a file (an order, a ship, a config edit), wait for the change to show in the API or the page before reading it. Reading it once right away races the source's refresh.
- Give fake secrets in tests plain values such as `fake-token-work-2`, or the `decoy-secret-<hex>` form that `.gitleaks.toml` allowlists. A key-shaped value such as `ghp_…` fails the secret scan, and clearing it from the branch takes a history rewrite.

### Order sources and settings

- The server masks secret settings, including values inside arrays, as `__admiral_unchanged__`, and restores them on save. A settings control keeps that marker intact: a list whose stored value holds the marker stays a JSON field. Decide from what the server actually masked (`maskSecrets` in `internal/sources/service.go`), not from schema keywords alone.
- A settings change or Restart reuses the source's runner (`internal/sources/supervisor.go`), so its panels' last orders and errors carry over to the new process. Adding a source, or removing and re-adding it, starts a new runner with none.
