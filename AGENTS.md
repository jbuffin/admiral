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
2. `designs/redesign/` and the [wireframe canvas](https://claude.ai/artifact/MS8gLZsMr1NJUiCjw6arqu): the reference for the new shell's look, layout and motion. `designs/prototype/index.html` and `designs/README.md` describe the old UI, which is frozen until the new shell replaces it.
3. [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what to build, as user stories and implementation decisions. Behavior wins over the designs; the designs win on visual detail.
4. `docs/adr/`. Open questions are GitHub issues. Do not build a part that depends on one until it is resolved and recorded.

Wherever the UI shows markdown, render it with the shared renderer in `web/src/app/markdown.ts` (`renderMarkdown`, or its restricted variants for short text). Never show it as raw text and never add a second renderer.

Touch must work on iPad Safari. Use the harnesses' official logos on flags; never redraw them.

### Tests

A test checks a behavior a person or caller can see: an API response, what the page shows, a ship's status, a file written. Refactoring the code under it leaves it green. Test each behavior once, at the lowest layer that sees it, and read the layer table in [`docs/agents/testing.md`](docs/agents/testing.md) before adding a test. An E2E test is a journey a real person takes through the new shell at `/app/` that crosses browser, server and tmux or a harness and that no lower layer can see; extend an existing journey before adding one. A component's states, validation and what a click changes with the server stubbed are component tests (`*.test.tsx`). A flaky test is a bug: fix the race, never add retries, longer timeouts or a skip.

### Comments

Match the comment density of the code around it; most functions need none. A comment says why, in one line: a hidden constraint, an invariant, a workaround with its link, or behavior that would surprise a reader. Names and code say what. Issue numbers, history and review notes go in the commit or PR, where they don't rot.
`make check` fails on an issue number (`#` and digits) in a Go or TypeScript comment.

### More docs

Read these when the task touches the area:

- [`docs/agents/codebase.md`](docs/agents/codebase.md): the index of the area files below, with the repo pointers.
  - [`codebase/packages.md`](docs/agents/codebase/packages.md): the Go packages and top-level directories. Read it before changing finding where a change goes.
  - [`codebase/ships-and-moor.md`](docs/agents/codebase/ships-and-moor.md): ship routes, statuses, mooring and PR actions. Read it before changing a ship route, `internal/ships` or `internal/moor`.
  - [`codebase/hooks-and-guard.md`](docs/agents/codebase/hooks-and-guard.md): `admiral hook`, the autopilot guard and ship env. Read it before changing `internal/cli/hook.go`, `guard*.go` or the launcher bundle.
  - [`codebase/launch.md`](docs/agents/codebase/launch.md): clone on launch, ship cap, locked resources, worktrees. Read it before changing `internal/launcher`.
  - [`codebase/crew-boats.md`](docs/agents/codebase/crew-boats.md): crew boats and subagents. Read it before changing crew handling or the crew bridge.
  - [`codebase/codex.md`](docs/agents/codebase/codex.md): Codex ships. Read it before changing Codex launches or `internal/shipconv` Codex reading.
  - [`codebase/outcomes-and-usage.md`](docs/agents/codebase/outcomes-and-usage.md): outcome log and usage. Read it before changing `internal/outcomes` or usage.
  - [`codebase/permissions.md`](docs/agents/codebase/permissions.md): permission log and allow rules. Read it before changing `internal/permlog` or allow rules.
  - [`codebase/routines-and-events.md`](docs/agents/codebase/routines-and-events.md): routines and the Lookout. Read it before changing `internal/routines` or the Lookout.
  - [`codebase/commitments-and-campaigns.md`](docs/agents/codebase/commitments-and-campaigns.md): commitments and campaigns. Read it before changing commitments or campaigns.
  - [`codebase/config-and-fleets.md`](docs/agents/codebase/config-and-fleets.md): fleets, fleet env, chat settings, update check. Read it before changing `internal/config` or a fleet route.
  - [`codebase/sources.md`](docs/agents/codebase/sources.md): order sources and the GitHub source. Read it before changing `internal/sources`.
  - [`codebase/capabilities-and-probes.md`](docs/agents/codebase/capabilities-and-probes.md): capabilities, probes, autopilot gate. Read it before changing a probe or a capability.
  - [`codebase/inbox.md`](docs/agents/codebase/inbox.md): the Needs You inbox. Read it before changing `internal/inbox`.
  - [`codebase/wake.md`](docs/agents/codebase/wake.md): the wake decision. Read it before changing `internal/ships/wake*.go`.
  - [`codebase/chat.md`](docs/agents/codebase/chat.md): AdmiralBot's chat session (also `chat-harnesses.md`, `chat-cards.md`). Read it before changing `internal/chat`, `internal/brief` or the chat fence.
  - [`codebase/devices.md`](docs/agents/codebase/devices.md): devices and pairing. Read it before changing `internal/auth`.
  - [`codebase/api-versions.md`](docs/agents/codebase/api-versions.md): API versions. Read it before changing a route's versioning.
  - [`codebase/new-shell.md`](docs/agents/codebase/new-shell.md): the new shell at `/app/`. Read it before changing `web/src/app/`.
  - [`codebase/old-ui.md`](docs/agents/codebase/old-ui.md): the frozen old UI. Read it before changing old-UI code.
  - [`codebase/idle-memory-and-setup.md`](docs/agents/codebase/idle-memory-and-setup.md): idle memory and the setup terminal. Read it before changing memory use or `admiral setup`.
- [`docs/agents/testing.md`](docs/agents/testing.md): what to test and at which layer, the test seams, `$ADMIRAL_HOME` isolation, the fake harness cue language and the faked LAN. Read it before writing or changing a test.
- [`docs/bridge-protocol.md`](docs/bridge-protocol.md): the bridge WebSocket messages. Read it before changing the bridge or `web/src/bridgeLink.ts`.
- [`docs/api.md`](docs/api.md): the HTTP and WebSocket shapes a client reads, starting with a ship's conversation. Read it before changing `internal/shipconv/` or a client of it.
- [`docs/cli.md`](docs/cli.md): every `admiral` command and the setup answers file. Read it before changing `internal/cli/`.
- [`docs/agents/releasing.md`](docs/agents/releasing.md): the Release workflow, its one-time setup and the manual fallback. Read it before cutting a release or changing `release.yml`.
- [`docs/personal-builds.md`](docs/personal-builds.md): building with the official harness logos. Read it before touching `web/logos/` or `web/src/Logos.tsx`.

### Before you push

PR CI is light: `make check` on Ubuntu (build, lint, and the unit tests that need neither tmux nor macOS). The e2e suites run on CI only in the release, through `macos.yml`, which you can also start by hand from the Actions tab. So your Mac is the gate: before you push a branch that changes code or tests, run `make e2e` on its head commit, and push once it passes.

A PR that changes code or tests carries **e2e evidence** in its body, under `## E2E evidence` (the PR template has the slot):

- The commit `make e2e` ran on (`git rev-parse --short HEAD`). It matches the PR's head, or the later commits are listed with why they need no rerun (docs only, for example).
- The Go e2e result line (`ok  github.com/jbuffin/admiral/e2e …`) and Playwright's summary (`N passed`, and any `skipped` or `flaky` lines), pasted from the output as they printed.

A docs-only PR says `Docs only` there instead.

A unit test that needs tmux or macOS skips under `go test -short`, the way `needTmux` in `internal/sessionhost/host_test.go` does, so `make check` stays green on Ubuntu.

Keep the `@critical` set to the flows Admiral cannot ship broken. The release runs them on a macOS runner whose virtual GPU resets under WebKit, so the full browser matrix flakes there (#94).

### Running the tests on a Mac

- Run one Playwright suite at a time on a machine. Several runs at once (each with its own WebKit workers and trace screenshots) can exhaust WindowServer's IOSurfaces and crash it, which logs you out. Local runs use 2 workers and no trace screenshots (`web/playwright.config.ts`); CI keeps the defaults.
- Admiral's tmux server outlives Admiral by design. A test that starts Admiral must stop it before `tmux -S <home>/tmux.sock kill-server`, or Admiral starts a new tmux and it leaks. In Go, register the kill-server cleanup before `t.Cleanup(stop)`; `stop` in `web/e2e/fixtures.ts` does this for Playwright.
- After seeding state through the API or a file (an order, a ship, a config edit), wait for the change to show in the API or the page before reading it. Reading it once right away races the source's refresh.
- Give fake secrets in tests plain values such as `fake-token-work-2`, or the `decoy-secret-<hex>` form that `.gitleaks.toml` allowlists. A key-shaped value such as `ghp_…` fails the secret scan, and clearing it from the branch takes a history rewrite.

### Order sources and settings

- The server masks secret settings, including values inside arrays, as `__admiral_unchanged__`, and restores them on save. A settings control keeps that marker intact: a list whose stored value holds the marker stays a JSON field. Decide from what the server actually masked (`maskSecrets` in `internal/sources/service.go`), not from schema keywords alone.
- A settings change or Restart reuses the source's runner (`internal/sources/supervisor.go`), so its panels' last orders and errors carry over to the new process. Adding a source, or removing and re-adding it, starts a new runner with none.
