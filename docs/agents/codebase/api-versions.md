# API versions

The `/api/v1` and `/ws/v1` aliases, the version route and the logo route. Read it before adding or versioning a route.

- `/api/v1/...` and `/ws/v1/...` answer exactly as `/api/...` and `/ws/...` do: status, headers and body. `Handler()` in `internal/server/server.go` strips the `/v1` before routing, so a module registers its routes once, unversioned, and every route (current and future) has a v1 path. The strip sits inside `guard.Middleware`, so v1 gets the same access control, with no path-specific auth code.
- `GET /api/version` (also `/api/v1/version`) returns `version`, `commit`, `api_versions` (`[1]`) and `capabilities`, with `Cache-Control: no-store`. It needs the same auth as the rest of the API.
- `capabilities` is an array of strings naming the server features this build serves, set in `cmd/admiral/main.go`. Each entry names a feature group, not a module (the bridge module serves two, `bridge` and `setup`; the lock route has none). They are server features, not harness capabilities (GLOSSARY.md, ADR 0035). Within v1 the list only grows:
  - `ships`: launch, list, move, dismiss and close crew boats on ships.
  - `sources`: order sources and their views.
  - `inbox`: the inbox items and their answers.
  - `launch-orders`: launch orders, favorites and the launch dock's directory and skill lists.
  - `bridge`: the bridge sockets on ships and crew boats.
  - `setup`: the setup terminal's routes.
  - `resources`: the resource units and their locks.
  - `chat`: the chat session.
  - `ship-input`: send a message to, interrupt and stop a ship, and send to or interrupt a crew boat (`POST /api/ships/<session>/{messages,interrupt,stop}` and `/crew/<name>/{messages,interrupt}`; see `docs/api.md`).
  - `conversation`: a Claude Code, Codex or Copilot ship's conversation (`GET /api/ships/<session>/conversation` and the WebSocket `conversation` messages; see `docs/api.md`).
    - It also covers a crew boat's (`GET /api/ships/{session}/crew/{name}/conversation` and the `crew` field of the WebSocket messages): `internal/shipconv` keys a subscription by session or by `{session}/crew/{name}`, and reads the boat's path fresh from its pane option (`@admiral-crew-transcript`), confined by `transcriptfs` and `ValidCrewTranscriptPath` only.
    - A Codex ship or boat is read the same way through the rollout adapter (`internal/conversation/codex.go`, selected by `Options.Format`) with `transcriptfs.OpenIn` on `<Codex home>/sessions/`; the hook's `SessionStart` records the path, and a watch re-reads the option about once a second to follow a new rollout.
- Bodies stay byte-identical, so a URL inside a body stays unversioned (`icon_url` in `/api/sources` and `/api/plugins` is `/api/plugins/<name>/icon`). The server does not rewrite them: the shell does, with `iconSrc` in `web/src/app/api.ts`, which turns a leading `/api/` into `/api/v1/`.
- The bridge socket's `join` carries an optional `api` version; see `docs/bridge-protocol.md`. v1 changes are additive only. The unversioned paths stay because the frozen old UI, which `internal/server` still serves outside `/app/`, calls them.
- `GET /api/logos/<harness>` (also `/api/v1/logos/<harness>`) serves `$ADMIRAL_HOME/logos/<harness>.svg` as `image/svg+xml`, with `X-Content-Type-Options: nosniff` and a Content-Security-Policy that allows no script. It answers 404 when the file is missing or `<harness>` isn't one `config.Harnesses()` returns (claude, copilot, codex; `unknown` is 404), and never reads outside that folder. It has no `capabilities` entry: clients fall back on the 404.
- Each fleet in the config view (`GET /api/config` and the WebSocket `config` message) carries an additive `color_index`: the fleet's position in a color sequence. A fleet with no `color` in config gets the raw counter `assignColors` used, not wrapped. A stored `color` that matches an old palette entry (case-insensitive, as `admiral setup` writes the default hex) gets that entry's position. A custom hex has no `color_index`. The client wraps it by the redesign sequence's length. `color` and `ink` stay the old palette's values.
