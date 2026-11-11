# API

The HTTP and WebSocket shapes a client reads. Every route is also served under `/api/v1/...` and `/ws/v1/...` (see "API versions" in `docs/agents/codebase.md`). The bridge sockets are in `docs/bridge-protocol.md`.

## Ships

### `GET /api/ships` (alias `GET /api/v1/ships`)

`{"ships": [...], "home": "..."}`: the ships, and the id of the home they belong to. Each ship is also in the WebSocket `{"type":"ships","ships":[...]}` message, which is sent on connect and again whenever any ship's fields change, its `tail` included.

`tail` is always present, a string of one line and at most 200 characters, `""` when nothing is readable. It comes from the ship's conversation, never from the terminal. Of the conversation's pending questions, tool calls and assistant items, the latest one decides:

- a pending question: its text;
- a tool call: its summary line, or its name when it has none;
- assistant text: its first non-empty line.

A pending question wins only while it is the latest of these, so a question an interrupted turn left pending does not pin the tail. Answered questions and every other item (user, turn end, compaction, notice) are skipped. Only Claude ships have a conversation adapter: a Codex or Copilot ship, a ship with no transcript yet and one whose transcript cannot be read have `tail: ""`.

### `POST /api/launch-orders/dirs` (alias `POST /api/v1/launch-orders/dirs`)

Body: `fleet`, `harness`, and either `source` and `order_id` (the server reads the order) or the `repo` and `text` the order would have. Answers `{"entries": [...], "selected": ..., "reason": ..., "clone": ...}`. The entries are the checkouts and search roots of the fleet, which include its `clone_root`.

`clone` is always present. It is `{"repo": "<host/owner/repo>", "path": "<target>"}` when the order's repo (else the first URL in its text) has no checkout in the fleet and gh can clone it, and `null` otherwise. `path` is `<clone_root>/<repo name>`, where `clone_root` defaults to the fleet's first search root. The folder name is the repo's name in lower case (`Acme/MyWidget` clones into `mywidget`). When `clone` is set, `selected` is `null` and `reason` is `"clone"`: launching clones the repo. When a file or folder already exists at `path`, `clone` carries `"exists": true`, nothing is preselected as a clone (`selected` and `reason` are as without a hint) and a launch there would be refused with 409, so a client can warn. Otherwise `selected` and `reason` are as before.

### `POST /api/ships` (alias `POST /api/v1/ships`)

Launches a ship. Body: `fleet`, `harness`, `skill` (`{"kind": "commodore" | "skill" | "none", "name": ...}`), `text`, `model`, `effort`, `autopilot`, `resources`, `source` and `order_id` (a launch from an order; the server reads the order's text), and either `dir` (a checkout or search root of the fleet) or `clone: true` with no `dir`, and optionally `clone_repo`, the `host/owner/repo` the form showed. A launch with `clone: true` derives the repo like the dirs route does, runs `gh repo clone <host/owner/repo> <temporary folder>` in the fleet's environment, moves the result to `<clone_root>/<repo name>` (lower case) and starts the ship there. The client never sends the target path. Only github.com and the host of a GitHub source the fleet has are cloned. A launch with no order is freeform: `text` is its prompt and `skill` may name a skill, which the harness runs with the text as its argument (`/skill text`, `$skill text` on Codex). `model` and `effort` are empty for the harness's own default. Claude's `model` is one of `sonnet`, `opus` and `haiku` and its `effort` one of `low`, `medium`, `high` and `max`; Codex takes any plain model name (letters, digits, `.`, `-`, `_`, `:`, never dash-led) and `low`, `medium` or `high`; Copilot takes a plain model name and no effort, since it picks its own.

Answers `201 {"ship": ...}`, or `{"error": "..."}` with:

- `400`: a `model` or `effort` the harness does not offer (the message names which), `dir` and `clone` were both sent ("send dir or clone, not both"), no repo can be derived, or the repo is not one gh can clone (a host that is not GitHub, a name that starts with `-`, a dot name);
- `409`: the repo already has a checkout in the fleet, a search root that is itself a checkout included (the message names it: launch there with `dir`), a folder already exists at the target, which is never touched, or `clone_repo` is set and differs from the repo the server derives ("the order's repo changed to X; reopen the form");
- `502`: gh failed, the message holds its stderr on one line with the credentials of any URL in it replaced by `***`, and nothing is left behind;
- the other launch refusals as before.

## A ship's conversation

The conversation of a Claude Code ship, read from the harness's own transcript file. `GET /api/version` lists the `conversation` capability when the server has it.

### How the file is found

The launcher starts a Claude Code ship with `--session-id <uuid>` and sets the pane option `@admiral-transcript` to `<unix ns> launch <path>` before the harness runs. `<path>` is `<config dir>/projects/<project slug>/<uuid>.jsonl`, where the config dir is the one Claude Code uses for the fleet's Claude dock (`CLAUDE_CONFIG_DIR`, else `~/.claude`). Claude Code may store the file under another project directory name (it shortens long ones), so when the computed path is missing the server looks for `<config dir>/projects/*/<uuid>.jsonl`.

A dock whose args, or an order whose extra args, already choose a session (`--session-id`, `--resume`, `-r`, `--continue`, `-c`) launches as before with no `--session-id` of ours and no option. That ship's state is `none`.

The option's text is untrusted: anything running in the ship can write it. The session id in the option must equal the `--session-id` in the command the pane was started with (tmux keeps that, and `set-option` from the ship cannot change it; only a respawn of the pane, which replaces the harness too, can); on a mismatch, or when the start command has no `--session-id`, the state is `none`. The server takes the file from the option's path, else from the `projects/*/<uuid>.jsonl` lookup above, and reads it only if it is a regular file under the harness's own `<config dir>/projects/` with no symlink on the way (ADR 0019). A file or directory that is a symlink is `unreadable`, never `ok`.

### `GET /api/v1/ships/<ship>/conversation[?before=<id>]`

```json
{"state": "ok", "items": [ ... ], "more": true}
```

- `state`:
  - `ok`: the file was read. `items` may be empty (a new session has only metadata lines).
  - `none`: the ship has no conversation Admiral can read: not a Claude Code ship, no `@admiral-transcript` of ours, or the file has not been written yet.
  - `unreadable`: the path is refused (a symlink, not a regular file) or nothing in the file parses.
  - `gone`: the file was read before and is not there now. This is kept in memory, so after an Admiral restart a missing file is `none` until it is seen again.
- `items`: at most 200, oldest first, the newest 200 without `before`. An item is the model in `internal/conversation` (`id`, `kind`, `text`, and the tool call or question it holds). Ids contain characters that need URL escaping in `before`.
- `before`: the id of an item; the page is the up to 200 items before it. An empty `before`, one that is not an item's id, or one older than the window the server reads is `400`.
- `more`: there are items older than the first one returned, within the window the server reads. It is `false` at the file's first item.
- An unknown ship is `404`. A dismissed ship is unknown. A ship anchored after its harness exited stays readable until it is dismissed.

Paging reaches back as far as a first read does: the last 8 MB of the file, at most 16 MB read per request. An older item is not reachable and `more` is `false` at the start of that window.

### WebSocket

A client sends:

```json
{"type": "conversation_open", "ship": "<ship>"}
{"type": "conversation_close", "ship": "<ship>"}
```

The server answers an open with `{"type": "conversation_opened", "ship": "<ship>"}` once the subscription is in effect. A client sends `conversation_open`, waits for `conversation_opened`, then reads the page over HTTP, so no append falls between the page and the pushes. From then on, items that are new or changed in the file go to the clients that opened the ship, and to no other:

```json
{"type": "conversation", "ship": "<ship>", "items": [ ... ]}
```

- A changed item is sent again under the same `id` (a tool call whose output came later); the client replaces its copy.
- A message holds at most 200 items.
- An open for an unknown ship, or one with no conversation, is ignored, with no reply.
- A client holds at most 16 ships open. A 17th open closes the oldest.
- A subscription ends on `conversation_close`, when the connection closes, or when the ship is dismissed.
- The server follows the file by offset, about four times a second while any client has the ship open.

## Harness capabilities

What each fleet's harnesses were probed to do (ADR 0035). These are not the server `capabilities` that `GET /api/version` lists. Both routes are also served under `/api/v1/`.

### `GET /api/harness-capabilities`

```json
{"fleets":[{"key":"work","name":"Work","harnesses":[
  {"harness":"claude","version":"2.1.300","detected_at":"2026-11-11T09:00:00Z",
   "features":[{"id":"autopilot","label":"Autopilot","capabilities":[
     {"id":"autopilot.guard_hook","label":"Pre-tool guard hook",
      "status":"passed","checked_at":"2026-11-11T09:00:05Z","detail":"..."}]}]}]}]}
```

Fleets and harnesses follow the config's order; features follow the table (judge, bot, restricted, autopilot). `status` is `passed`, `failed` or `not_checked`, against the last detected `version`. `version` is `""` when none was detected, and the guard's `detail` then says why. `checked_at` and `detected_at` are `null` when there is no time.

### `POST /api/harness-capabilities/check`

Body `{"fleet":"work","harness":"claude"}`. Runs the probes for that harness and waits (up to two minutes). It answers 200 with the harness object above plus `"fleet"`. 400 for a bad body, 404 for an unknown fleet or a harness the fleet has no dock for, 502 with `{"error":...}` when the harness's `--version` fails.
