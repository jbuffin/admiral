# API

The HTTP and WebSocket shapes a client reads. Every route is also served under `/api/v1/...` and `/ws/v1/...` (see "API versions" in `docs/agents/codebase.md`). The bridge sockets are in `docs/bridge-protocol.md`.

## Ships

### `GET /api/ships` (alias `GET /api/v1/ships`)

`{"ships": [...], "home": "..."}`: the ships, and the id of the home they belong to. Each ship is also in the WebSocket `{"type":"ships","ships":[...]}` message, which is sent on connect and again whenever any ship's fields change, its `tail` included.

A moored ship has `status` `moored` and no tmux session: it is listed from its record in `$ADMIRAL_HOME/moored/`, with `prs`, `moored_at` (RFC 3339), `polled_at` and `stale`, which no other ship carries. `prs` is a list of `{url, state, review, checks, mergeable, conflicts}`: `state` is `OPEN`, `MERGED` or `CLOSED` (a PR GitHub answers NOT_FOUND for counts as `CLOSED`; a NOT_FOUND on its repository says nothing about it, so its ship stays moored and `stale`); `review` is GitHub's `reviewDecision` (`APPROVED`, `CHANGES_REQUESTED`, `REVIEW_REQUIRED`, or `""` for none); `checks` is the status check rollup (`SUCCESS`, `FAILURE`, `ERROR`, `PENDING`, `EXPECTED`, or `""`); `mergeable` is `MERGEABLE`, `CONFLICTING` or `UNKNOWN`; `conflicts` is true only for an `OPEN` PR that is `CONFLICTING`. Every key is always served: before the first poll answers a PR has its `url`, `state` `OPEN`, empty chips and `conflicts` false. `polled_at` (RFC 3339) is the last poll that answered, absent before one has. `stale` is omitted while false and true when the latest poll failed to reach GitHub: the chips are the last known ones, and a failed poll never ends mooring. Its `session` is its slug, `dir` its checkout, `crew` and `subagents` empty and `launched` empty.

`tail` is always present, a string of one line and at most 200 characters, `""` when nothing is readable. It comes from the ship's conversation, never from the terminal. Of the conversation's pending questions, tool calls and assistant items, the latest one decides:

- a pending question: its text;
- a tool call: its summary line, or its name when it has none;
- assistant text: its first non-empty line.

A pending question wins only while it is the latest of these, so a question an interrupted turn left pending does not pin the tail. Answered questions and every other item (user, turn end, compaction, notice) are skipped. Only Claude ships have a conversation adapter: a Codex or Copilot ship, a ship with no transcript yet and one whose transcript cannot be read have `tail: ""`.

### `POST /api/moored/refresh` (alias `POST /api/v1/moored/refresh`)

Asks the poller to poll every moored ship's PRs now and answers `202` at once. A kick during a poll joins the next one, so polls never overlap. Admiral also polls every 2 minutes (`ADMIRAL_MOOR_POLL`).

### `POST /api/ships/<session>/dismiss` and `/unfollow`

Dismiss ends an anchored ship or a moored one. A moored ship's record goes, its name is free and one `unmoored` outcome with reason `dismissed` is written; the answer is `200 {"dismissed": "<session>"}`. `unfollow` (Don't follow reviews) ends a moored ship the same way with reason `unfollowed` and answers `200 {"unfollowed": "<session>"}`. An unknown ship, or one already ended, is `404`; a live ship that is not moored gets the `409` Dismiss gives a ship that is not anchored.

### `POST /api/launch-orders/dirs` (alias `POST /api/v1/launch-orders/dirs`)

Body: `fleet`, `harness`, and either `source` and `order_id` (the server reads the order) or the `repo` and `text` the order would have. Answers `{"entries": [...], "selected": ..., "reason": ..., "clone": ...}`. The entries are the checkouts and search roots of the fleet, which include its `clone_root`.

`clone` is always present. It is `{"repo": "<host/owner/repo>", "path": "<target>"}` when the order's repo (else the first URL in its text) has no checkout in the fleet and gh can clone it, and `null` otherwise. `path` is `<clone_root>/<repo name>`, where `clone_root` defaults to the fleet's first search root. The folder name is the repo's name in lower case (`Acme/MyWidget` clones into `mywidget`). When `clone` is set, `selected` is `null` and `reason` is `"clone"`: launching clones the repo. When a file or folder already exists at `path`, `clone` carries `"exists": true`, nothing is preselected as a clone (`selected` and `reason` are as without a hint) and a launch there would be refused with 409, so a client can warn. Otherwise `selected` and `reason` are as before.

### `POST /api/ships` (alias `POST /api/v1/ships`)

Launches a ship. Body: `fleet`, `harness`, `skill` (`{"kind": "commodore" | "skill" | "none", "name": ...}`), `text`, `model`, `effort`, `autopilot`, `resources`, `source` and `order_id` (a launch from an order; the server reads the order's text), and either `dir` (a checkout or search root of the fleet) or `clone: true` with no `dir`, and optionally `clone_repo`, the `host/owner/repo` the form showed. `crew` is a list of member names to pin, only on a Commodore launch (see below). A launch with `clone: true` derives the repo like the dirs route does, runs `gh repo clone <host/owner/repo> <temporary folder>` in the fleet's environment, moves the result to `<clone_root>/<repo name>` (lower case) and starts the ship there. The client never sends the target path. Only github.com and the host of a GitHub source the fleet has are cloned. A launch with no order is freeform: `text` is its prompt and `skill` may name a skill, which the harness runs with the text as its argument (`/skill text`, `$skill text` on Codex). `model` and `effort` are empty for the harness's own default. Claude's `model` is one of `sonnet`, `opus` and `haiku` and its `effort` one of `low`, `medium`, `high` and `max`; Codex takes any plain model name (letters, digits, `.`, `-`, `_`, `:`, never dash-led) and `low`, `medium` or `high`; Copilot takes a plain model name and no effort, since it picks its own.

Answers `201 {"ship": ...}`, or `{"error": "..."}` with:

- `400`: a `crew` that pins on a launch whose skill is not Commodore, a name that is not a lowercase slug of at most 64 characters or is given twice (checked before any clone), a name no layer resolves, or a resolver error in any file of the layers, even an unpinned one (the message lists them); a `model` or `effort` the harness does not offer (the message names which), `autopilot` on a dock whose harness `admiral guard` does not cover (only Claude today) or whose guard probe has not passed in the fleet (the Inbox gets a notice, and a Check again in Preferences runs the probe), a `resources` name the config does not define, `dir` and `clone` were both sent ("send dir or clone, not both"), no repo can be derived, or the repo is not one gh can clone (a host that is not GitHub, a name that starts with `-`, a dot name), or `source` is `campaign`, whose launches go through `POST /api/campaigns/<id>/launch`;
- `409`: a resource in `resources` is held by another ship (`resource "db" is ...`), `autopilot` when the underway and needs-you ships are already at `max_ships` (`autopilot is at its cap of N ships`), the repo already has a checkout in the fleet, a search root that is itself a checkout included (the message names it: launch there with `dir`), a folder already exists at the target, which is never touched, or `clone_repo` is set and differs from the repo the server derives ("the order's repo changed to X; reopen the form");
- `404`: the fleet is not found; `400`: the fleet has no such dock or no search roots, `dir` is missing, not a directory or not a checkout or search root of the fleet, a skill kind is unknown or a skill name is empty or holds whitespace, Commodore or the dock's executable is not found, or the command line is too long for tmux; `409`: the order already has a ship, or its URL names a GitHub issue that any ship holds, listed in any status or still launching (`this issue already has a ship (<name>)`; the ship leaves the claim when it is dismissed or sails off); `503`: tmux is not on PATH;
- `502`: gh failed, the message holds its stderr on one line with the credentials of any URL in it replaced by `***`, and nothing is left behind;

### Launching as a crew member on `POST /api/ships`

`member` (a crew member name) launches the ship as that member, and only with `skill` `{"kind": "none"}` (Just launch); with any other skill, or sent next to `crew`, it is a `400`, as is a name that is not a lowercase slug of at most 64 characters (checked before any clone). Admiral resolves it over the same layers as pins plus the fleet's memory layer. The member's `harness` is a lock: a `harness` in the body that differs is a `400`, and the fleet needs a dock for the final harness. Its `model` goes after the dock's `args` and Admiral's own model flags, before any extra args, as `--model <m>` (`-m` on Codex), unless it breaks the model name rule (dropped, and the reason goes to the server log) or the dock's `args` already set a model (`400`); a `model` in the body next to a member's model is a `400`. The prompt is the member's instructions, its notes block (`### Every repo` and `### This repo (<owner/repo>)`, the repo read from the launch directory's `origin`), then `text`, joined by a blank line, empty parts skipped. A fix checker takes the body's harness and model and gets no instructions or notes; a member's `skills` are ignored. Warnings go to the server log, not the answer. A member no layer resolves (an ad hoc member is one: ad hoc is not a layer `member` reads), or a resolver error in any layer file, is a `400`.

### Pinned crew members on `POST /api/ships`

`crew` (a list of member names; empty or absent pins nothing) is the only way a launch pins members: the launch text is never read for them. Admiral resolves the names itself over the installed Commodore's `crew/` directory (beside its `SKILL.md`, empty when it has none), the fleet's crew directory `$ADMIRAL_HOME/fleets/<fleet key>/crew/` and the launch directory's `.commodore/crew/`, a later layer replacing an earlier member of the same name. It then writes a byte-exact copy of each pinned member's file as `<name>.md` into the ship's run layer, `$ADMIRAL_HOME/run-crew/<session>/` (0700), and gives the ship `ADMIRAL_RUN_CREW` with its absolute path. A launch with no pins has no `ADMIRAL_RUN_CREW`, whatever a fleet's `env` says, and a crew boat keeps its ship's value. The run layer is removed with the ship's report and inbox, and cleared when a ship name is reused.

A refusal for pins is a `400` before any session exists. One that comes after a `clone` launch has cloned (a name no layer resolves, a resolver error) leaves the clone in place, like any checkout.

### `GET /api/launch-orders/crew` (alias `GET /api/v1/launch-orders/crew`)

Query: `fleet`, `harness` and optionally `dir` (a checkout or search root of the fleet, checked as `POST /api/ships` checks it; without it the repo layer is left out, as for a launch that has not cloned yet). Answers `{"members": [...], "warnings": [...], "errors": [...]}`: each member is `{"name", "role", "pick-me-when", "layer", "harness"}` (`layer` is `built-in`, `fleet` or `repo`; `harness` is the lock, or `null`), `warnings` and `errors` are the resolver's diagnostics. When `errors` is not empty, `members` is empty and a launch that pins would be refused. A harness with no installed Commodore has an empty built-in layer, not an error. `404`: the fleet is not found; `400`: no such dock or `dir` is not in the fleet; `409`: the fleet has no search roots.

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

## Campaigns

A campaign is a GitHub issue with sub-issues, followed. Admiral reads the tree through the fleet's GitHub source and never writes to GitHub (ADR 0027). All routes are also served under `/api/v1/`. Errors are `{"error": "..."}`.

A campaign is:

```json
{"id":"3f9c0a41b7d2e8a5","url":"https://github.com/acme/widgets/issues/20","fleet":"work",
 "work_repo":"","ended":false,"last_read":"2026-11-11T09:00:00Z","read_error":"",
 "progress":{"closed":1,"total":4},"counts":{"ready":2,"blocked":1,"done":1},
 "root":{"number":20,"title":"...","url":"...","repo":"acme/widgets","closed":false,"parent":true,
  "status":"","blockers":[],"ship":"","mode":"","skill":{"kind":"","name":"","args":""},
  "children":[{"number":21,"status":"ready","mode":"with-you","skill":{"kind":"none","name":"","args":""},"...":"..."}]}}
```

`status` of a child is `ready`, `blocked` (`blockers` lists `{"url","title"}` of the open ones), `underway` (`ship` names the ship holding the issue), `claimed-no-ship` (assigned, no ship) or `done`. A nested parent has `"parent": true` and no status. `mode` is `with-you` or `without-you`; `skill` is what the child's ship starts on (`commodore`, `skill` with a `name`, or `none`), `args` the text after the skill name. `unreadable` appears on a node GitHub counts more sub-issues for than were read. `last_read` is when the last good read finished, and `read_error` the last failed read's message, empty after a good one: a failed read keeps the last tree. The list route leaves `root` out.

A campaign also carries:

- `title`, `number` and `repo` (`https://host/owner/repo`) of the root issue, so the list can name it without the tree;
- `followed_at`, and `ended_at` once it has ended (absent while followed);
- `ready_with_you` and `ready_without_you`: the launchable `ready` children by their resolved mode;
- `reading`: `true` while a reread of this campaign is in flight, and `last_tried`, the time of the last attempt, good or bad, kept across restarts (absent before any). A failed attempt moves `last_tried` and not `last_read`, so a poller tells a read still under way from one that failed again.

Each node carries `labels` (names, `[]` when none; a parent's too, so the root's kind shows). A child also carries `default_mode` and `default_skill`, what ADR 0027's table gives it with no override, and `overridden: true` (absent otherwise) when the campaign has an override setting its mode or skill (even to the default), so a client can offer Reset. A blocker is `{"url","title","capped"}`; `capped: true` marks the stand-in for the `Blocked by` lines past the cap (its `url` is the issue that carries them, not a blocker; a blocker GitHub could not be read for, past the lookup cap, is not marked), and is absent on a real blocker.

### `GET /api/campaigns[?ended=true|false]`

`{"campaigns": [...]}`, active and ended in the order they were followed; `ended` filters.

### `POST /api/campaigns`

Body `{"reference": "<issue URL or owner/repo#n>"}`. `owner/repo#n` means github.com. Reads the tree, stores the campaign and launches nothing. `201` with the campaign; `200` with the campaign already followed for that issue, ended included. `400` for a reference that is not an issue (the message says what to paste); `422` when no fleet owns the repo, the fleet has no GitHub source for the issue's host (a host is compared ignoring case and must be the source's own), or the read failed.

### `GET /api/campaigns/<id>`

The campaign with its tree. Opening it rereads in the background when the last attempt is 30 seconds old or more (never for an ended campaign). `404` for an unknown id.

### `POST /api/campaigns/<id>/unfollow`

Ends the campaign at once; it stays readable and is listed with `ended: true`. Answers the campaign.

### `PUT /api/campaigns/<id>/override`

Body `{"child_url", "mode", "skill": {"kind","name","args"}}`; an empty `mode` and no `skill` clear the override. `404` when the issue is not in the campaign, `400` for a parent, a mode or a skill kind that does not exist. Answers the campaign.

### `PUT /api/campaigns/<id>/work-repo`

Body `{"repo": "host/owner/repo"}` or its URL; `""` clears it. It is stored and answered as `https://host/owner/repo`, with any userinfo or query dropped. The work repo is the checkout a launch preselects, or the repo it clones, for children filed in the campaign's own repo; a child in another repo keeps its own. `400` for a value that is not a repo. Answers the campaign.

### `POST /api/campaigns/<id>/launch`

Body `{"child_url", "harness", "dir" | "clone": true, "clone_repo", "start_anyway", "model", "effort"}`; `clone_repo` is the `host/owner/repo` the form showed, as for `POST /api/ships`, and when it is omitted the server uses the work repo (or the child's own repo) it would show. It launches through the same path as `POST /api/ships`, so its refusals are the same, with the source `campaign` and `<campaign id>/<child issue URL>` as `order_id`, so a child in two followed campaigns is two orders, each answered with its own campaign's skill, text, repo and panel (the campaign id). The claim is still by the child's issue URL alone: one ship holds the child in every campaign. The server sets the ship's title, `url`, text and skill; the skill is the child's resolved one, `autopilot` is always off, and any such field in the body is ignored. A launch rereads the tree first when the last read is over a minute old. Answers `201 {"ship": ...}`, or:

- `404`: the issue is not in the campaign;
- `400`: it is a nested parent;
- `409`: the child is done, a ship holds it (the message names the ship), it is blocked (the message lists the blockers' URLs), it is assigned with no ship and `start_anyway` is not true, the campaign has ended, or any `POST /api/ships` conflict;
- the launcher's own statuses otherwise.

What the launcher puts in the prompt: the `skill` runs with the text as its argument (`/skill text`, `$skill text` on Codex). The text is the child's `skill.args` when set (`<map URL> <ticket URL>` for `/wayfinder`), else the ticket URL; a child with no skill gets its title and URL.

A campaign ends when the parent and every issue under it, nested parents included, are closed, and none has sub-issues that could not be read; or when it is unfollowed. It is reread when a child's ship anchors or sails off, when its page is opened, and every five minutes.

## Harness capabilities

What each fleet's harnesses were probed to do (ADR 0035). These are not the server `capabilities` that `GET /api/version` lists. Both routes are also served under `/api/v1/`.

### `GET /api/harness-capabilities`

```json
{"fleets":[{"key":"work","name":"Work",
 "judge":{"harness":"claude","lookout_model":"haiku","strong_model":"sonnet","json_schema":true,"call_cap":true},
 "harnesses":[
  {"harness":"claude","version":"2.1.300","detected_at":"2026-11-11T09:00:00Z",
   "features":[{"id":"autopilot","label":"Autopilot","capabilities":[
     {"id":"autopilot.guard_hook","label":"Pre-tool guard hook",
      "status":"passed","checked_at":"2026-11-11T09:00:05Z","detail":"..."}]}]}]}]}
```

Fleets and harnesses follow the config's order; features follow the table (judge, bot, restricted, autopilot). `status` is `passed`, `failed` or `not_checked`, against the last detected `version`. `version` is `""` when none was detected, and the guard's `detail` then says why. `checked_at` and `detected_at` are `null` when there is no time.

`judge` is the fleet's judge harness, from `judge.Choose` on the stored probes: the first of claude, codex and copilot that proved no tools and no network, with its Lookout and strong models and whether it proved a JSON schema flag and a per-call cap. A fleet with no such harness has `{"harness":null,"reason":"..."}`, the reason naming each harness and what it lacks, and runs no judge calls.

`autopilot.advisor` is never stored. Every harness of a fleet reads `not_checked` until some harness of the fleet has a stored judge result, then `passed` (naming the judge harness) when the fleet has a judge harness and `failed`, with the `judge.reason`, when it has none, so it follows the other harnesses' probes. The gate refuses in every state but `passed`. Autopilot on a harness is refused until it passes.

### `POST /api/harness-capabilities/check`

Body `{"fleet":"work","harness":"claude"}`. Runs the probes for that harness and waits (up to eight minutes: the guard run and up to four judge calls of up to 90 seconds each). It answers 200 with the harness object above plus `"fleet"` and the fleet's `"judge"` object, which the check may just have changed. 400 for a bad body, 404 for an unknown fleet or a harness the fleet has no dock for, 502 with `{"error":...}` when the harness's `--version` fails.
