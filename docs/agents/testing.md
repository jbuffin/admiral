# Testing

What to test, where each test goes, and how the tests are set up.

## What to test

Each behavior gets one test, at the lowest layer that can see it:

| The behavior | Its layer |
| --- | --- |
| Validation, parsing, config shape and migration, state logic, masking secrets | Go unit test in the package |
| What a route returns for a request | Go `httptest` against the handler |
| The new shell's UI state logic (stores, ordering, protocol replay) | Vitest (`*.test.ts`, Node) |
| What one component shows for a state, and what a click or key changes on screen when the server's answer is stubbed | Component test (`*.test.tsx`, Vitest with happy-dom) |
| A journey that crosses browser, server and tmux or a harness, and that no lower layer can see | E2E: Go `e2e/` for server and tmux, Playwright for the page |

A test earns its place when it would catch a bug a person could hit:

- Assert outcomes: the response, the page, the ship's status, the file on disk. A test that still passes after the code under it breaks, or fails after a refactor that keeps the behavior, is a change detector; rewrite it or delete it.
- Take expected values from the spec, the story or a worked example, never by recomputing them the way the code does.
- Pick one case per distinct rule. Spelling variants of one flag, every bidi code point, every way to write the same argument: one representative each.
- Test the inputs real callers send. The boundary validates; test it there, and trust the code behind it.
- Mock only what Admiral doesn't control: `gh`, the network, the clock. Use the real binary, tmux and the fake harness for the rest.
- Wait on a condition (a file, an API state, a locator), never on a fixed sleep. To show that something does not happen, poll it for its whole window after the test has seen what it starts from: `holdThat` for `hold`, `holdWindow` for a window of the test's own, or `holdClean` when the check returns the reason it failed (`e2e/harness_test.go`). A negative check that passes after one look proves nothing.
- A red test describes the behavior it expects. Once the code exists, the test file carries no header spelling out the interface it assumed.
- Text, class names, colors, layout and focus order are the designs' job. A Playwright test finds elements by role and accessible name, and asserts what the person sees happen.

### Component tests

A component test renders one component of the new shell in Node, under happy-dom with `@testing-library/preact`, and runs in `make check` with no browser. Write one for:

- What a component shows for a given state, including its empty and error states.
- A form's validation, and what enables or disables a control.
- What a click or key changes on screen, with `fetch` stubbed (`vi.stubGlobal('fetch', …)`) so the test also sees the request body.

Find elements by role and accessible name, as a person would, and assert what shows. Start from `src/app/Composer.test.tsx` and `src/app/QuestionCall.test.tsx`.

- Stub `fetch` or mock `./api` in every test. happy-dom's `fetch` is real, and no test touches a network.
- Module-level state (the skills cache, global signals) persists within a file. Reset it with `vi.resetModules` or give each test its own inputs, so no test depends on order.
- Never import `src/app/main.tsx` (CSS, router, WebSockets, the service worker). A component that reads a global store or opens a WebSocket (`Shell`, `ShipView`) is covered by E2E or by its view model in Vitest.
- happy-dom has no layout and no real touch. Whether a thing fits, scrolls sideways, sits under a sheet or takes a tap stays in Playwright.

### Playwright and E2E

An E2E test is a journey a person takes that crosses the browser, the server and tmux or a harness, and that no lower layer can see. The Playwright journeys are the tests tagged `@critical`, a short list of flows Admiral cannot ship broken; find them with `grep -n '@critical' web/e2e/*.spec.ts` or `npm --prefix web run e2e -- --list --grep @critical`, and a journey missing from that list does not belong in Playwright. A new E2E test needs a reason no lower layer works, stated in the PR: extend the journey that already visits the screen first, and move a check that only needs a component's state down to a component test.

An E2E test is reliable when:

- It waits on a condition (a locator, an API state, a file), never a fixed sleep, and a negative check holds for its whole window (see above).
- It owns everything it touches: its own Admiral, `$ADMIRAL_HOME`, tmux socket and port, with no state shared with another test and no order it depends on.
- It ends what it starts, in order: Admiral first, then `tmux -S <home>/tmux.sock kill-server` (`stop` in `web/e2e/fixtures.ts` does both).
- Its data is deterministic: fixed names and seeds, no random value a check depends on, and no real clock where a fake one will do.
- It uses no real network and no real home (see `testing/fakes.md` for the network part), and waits after seeding state as AGENTS.md's "Running the tests on a Mac" says.
- It reads nothing about the Mac it runs on: no installed app, no tool on `PATH` beyond tmux, git and node, nothing under `~/.config`. The release runs on a clean runner, so a test that leans on your Mac passes for you and fails every release. A test-build seam (`cmd/admiral/*_e2e.go`) answers from its variable alone and, unset, gives the clean runner's answer, never the machine's.

A flaky test is a bug. Find the race and fix it. Never add a retry, a longer timeout or a skip to make it pass, and prove a fix with repeated runs.

Playwright drives the new shell at `/app/` only; the old UI is frozen and has no browser tests.

## How the tests are set up

Admiral keeps its data in `$ADMIRAL_HOME` (default `~/.admiral/`). Every test starts the binary with a fresh temporary one on a free port, so tests never touch yours.

Every Go e2e test owns its home, tmux socket and port, so the tests run in parallel: each top-level test calls `t.Parallel()`, and `make e2e` runs `E2E_PARALLEL` (4) at a time. The packaging tests stay serial, because they run `make release` in the checkout.

The e2e harness shortens Admiral's production delays through environment variables that take a Go duration (`internal/delay`):

- `ADMIRAL_WAKE_DEBOUNCE` (10 min): how long the newest waking event must have waited before a moored ship wakes. The e2e sets it small and writes the review's `submittedAt` just after the ship moors (`fakeGH.prEvents`).
- `ADMIRAL_SAIL_OFF_DELAY` (3 s): every Admiral in the suite runs with `sailOff` (1 s).
- `ADMIRAL_NO_HOOK_WAIT` (10 s): set only by the tests of the no-hook rule.
- `ADMIRAL_CALL_TIMEOUT` (10 s, a plugin's time to answer): set only by the tests of a hung plugin.
- `ADMIRAL_BRIDGE_IDLE` (30 s): set only by the tests of the bridge's linger.
- `ADMIRAL_WALL_CLOCK_MINUTE` (1 min): the unit of a fleet's `wall_clock_minutes`, which is also what sets how soon the scheduler checks a worker ship's deadline. The wall-clock e2e sets `3s`.
- `ADMIRAL_CHAT_RESET_IDLE` (10 min): AdmiralBot's short idle window before a scheduled reset; the long window is 6 times it. Set only by the tests of scheduled resets.
- `ADMIRAL_CHAT_FLUSH_WAIT` (3 min): how long a reset or an idle New chat waits for the hidden flush turn. Set only by the tests of an abandoned flush.
- `ADMIRAL_UPDATE_FIRST_CHECK` (1 min): how long after start the update check makes its first request. Set by the update badge journey, with the two seam variables below.

A test sets its own value through `world.extra`.

Tests sit at two seams. `e2e/` (Go) starts `bin/admiral` and drives it over HTTP and WebSocket. Its packaging checks (the release tarball, the install script, the formula and the service) carry the `packaging` build tag: `make e2e` leaves them out and the release runs them (see [releasing](releasing.md)). `web/e2e/` (Playwright, WebKit, touch) loads the new shell at `/app/` in two projects: `ipad-landscape` (1194x834) runs every spec but the `*-phone.spec.ts` ones, and `phone` (402x874) runs only those. Vitest covers the new shell's pure UI state logic and, in `*.test.tsx` under happy-dom, its components; the frozen old UI has one vitest, `web/src/ships.test.ts`, which checks that its ship feed drops a moored ship.

- A fresh git worktree has no `web/node_modules`: run `npm --prefix web ci` before `make build`, or the build fails with `vite: command not found` and an e2e run then fails on `bin/admiral not found`.
- `go build -tags e2e -trimpath -o bin/admiral ./cmd/admiral` builds an e2e binary without the web build, because `web/dist/.gitkeep` is tracked (`//go:embed all:dist`, `web/embed.go`). It embeds a stale or empty UI, so it serves the Go e2e only, not `npm --prefix web run e2e`.
- Packages with many tests keep their setup in `new*Rig` helpers; read the package's test files for one before building your own. `internal/routines` has `newRig` (`routines_test.go`) for Store tests and `newHarness` (`scheduler_test.go`) for scheduler, events, digest and quota tests. `internal/ships` has `newWakeRig(t, maxShips)` (`wake_tmux_test.go`), its method `mustLaunch(t, mod)` (`launchkey_test.go`) and `outcomeLines(t, home, ship, event)` (`actor_test.go`).

The push path is tested against `internal/push/pushtest` (a fake TLS push service, a subscriber that decrypts per RFC 8291 and a VAPID verifier). Unit tests hand the sender that service's client. The Go e2e starts `bin/admiral` with `ADMIRAL_PUSH_TEST_ROOTS=<path to a PEM file holding the fake's certificate>`, which only a `-tags e2e` build honours (`make build` does; `make release` does not, and a packaging test checks the release lacks the name), and which also lifts the refusal of loopback destinations for that run.

The update check never reaches GitHub in a test. Unit tests give `update.Options` a `BaseURL` (an `httptest` server) and a fake `Clock` whose `After` channels the test fires by hand. A `-tags e2e` build requests nothing unless `ADMIRAL_UPDATE_TEST_URL` (a local releases origin) and `ADMIRAL_UPDATE_TEST_VERSION` (the running version, since `make build` is `dev`) are both set, which the update badge journey in `web/e2e/shell.spec.ts` does with a Node server.

Test browsers are silent: the `browser` fixture in `web/e2e/fixtures.ts` gives every context an init script that ends the page's Web Audio graph at a gain-0 node, and `make check` fails a spec that imports `test` from `@playwright/test` or launches its own browser.

The Playwright journey for Web Push (`web/e2e/shell.spec.ts`, `installed app`, helper `web/e2e/push.ts`) fakes the browser's side: WebKit under Playwright has no `PushManager` and a real subscription would point at Apple's or Google's service. An init script stubs `PushManager`, `ServiceWorkerRegistration.prototype.pushManager` and `Notification` (permission granted, state in `localStorage` so a reload keeps it), and its `subscribe` hands out an endpoint on an HTTPS fake push service in Node (an openssl certificate in `admiral.home`, passed to Admiral as `ADMIRAL_PUSH_TEST_ROOTS`). The test holds the subscription's key pair and decrypts what the Mac posts. The worker's `push` and `notificationclick` handlers are not run in a browser; vitest covers their pure parts and `swBuild.test.ts` checks the built `sw.js` carries them.

`internal/crew/testdata/crew-resolution/` is a byte-exact copy of the shared fixtures in `jbuffin/skills` (symlinks kept; `SOURCE` names the commit). Re-sync it with `internal/crew/testdata/sync.sh <skills checkout>`.

Tests that launch ships, need orders, `gh`, `tailscale` or a LAN use the fakes described in [`testing/fakes.md`](testing/fakes.md).
