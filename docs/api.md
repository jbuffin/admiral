# API

The HTTP and WebSocket shapes a client reads. Every route is also served under `/api/v1/...` and `/ws/v1/...` (see "API versions" in `docs/agents/codebase.md`). The bridge sockets are in `docs/bridge-protocol.md`.

## Ships

### `GET /api/ships` (alias `GET /api/v1/ships`)

`{"ships": [...], "home": "..."}`: the ships, and the id of the home they belong to. Each ship is also in the WebSocket `{"type":"ships","ships":[...]}` message, which is sent on connect and again whenever any ship's fields change, its `tail` included.

A ship's `url` is its issue. A ship that holds several issues (a slice) also carries `issues`, every issue URL in order with `url` the first; a single-issue ship omits `issues`. The moored record keeps the list, so a moored slice ship carries it too.

A moored ship has `status` `moored` and no tmux session: it is listed from its record in `$ADMIRAL_HOME/moored/`, with `prs`, `moored_at` (RFC 3339), `polled_at`, `stale`, `no_resume` and `waiting`, which no other ship carries. `waiting` is a string, present while a feedback round is due and has not started: `Waiting for room under the cap`, `Waiting for <resource> (busy|missing|pending)`, `Waiting for the current wake to finish`, `Waiting for Admiral's terminal host`, or `Can't wake: <the refusal>` for one that a later poll won't clear. It clears when a poll answers and does not decide to wake, on a successful wake and when the mooring ends; a poll that fails to reach GitHub keeps it. `prs` is a list of `{url, state, review, checks, mergeable, conflicts, ready}`: `state` is `OPEN`, `MERGED` or `CLOSED` (a PR GitHub answers NOT_FOUND for counts as `CLOSED`; a NOT_FOUND on its repository says nothing about it, so its ship stays moored and `stale`); `review` is GitHub's `reviewDecision` (`APPROVED`, `CHANGES_REQUESTED`, `REVIEW_REQUIRED`, or `""` for none); `checks` is the status check rollup (`SUCCESS`, `FAILURE`, `ERROR`, `PENDING`, `EXPECTED`, or `""`); `mergeable` is `MERGEABLE`, `CONFLICTING` or `UNKNOWN`; `conflicts` is true only for an `OPEN` PR that is `CONFLICTING`; `ready` is true when the PR is `OPEN`, `APPROVED`, its checks are `SUCCESS`, it is `MERGEABLE` and its merge state is not `BLOCKED`, `BEHIND`, `DIRTY` or `DRAFT`, so a repository with no required review or no checks never reads ready. Every key is always served: before the first poll answers a PR has its `url`, `state` `OPEN`, empty chips and `conflicts` false. `polled_at` (RFC 3339) is the last poll that answered, absent before one has. `stale` is omitted while false and true when the latest poll failed to reach GitHub: the chips are the last known ones, and a failed poll never ends mooring. `no_resume` is omitted while false and true when the record has no valid resume text (`moor.ValidResume`), so the ship cannot be woken and a wake answers 409. A moored ship also carries `stack_ready` (every PR is `ready`; omitted while false), `ready_up_to` (the URL of the highest layer, in the order of `prs`, up to which every layer is `ready`; omitted when the lowest layer is not) and `merge_method` (the repository's default, `merge`, `squash` or `rebase`, asked of GitHub once per repository and fleet; omitted until a poll has it). Its `session` is its slug, `dir` its checkout, `crew` and `subagents` empty and `launched` empty.

Each boat in a ship's `crew` also carries `role` and `conversation` (see "A crew boat's conversation").

`tail` is always present, a string of one line and at most 200 characters, `""` when nothing is readable. It comes from the ship's conversation, never from the terminal. Of the conversation's pending questions, tool calls and assistant items, the latest one decides:

- a pending question: its text;
- a tool call: its summary line, or its name when it has none;
- assistant text: its first non-empty line.

A pending question wins only while it is the latest of these, so a question an interrupted turn left pending does not pin the tail. Answered questions and every other item (user, turn end, compaction, notice) are skipped. Only Claude Code, Codex and Copilot ships have a conversation adapter: a ship with no transcript yet and one whose transcript cannot be read have `tail: ""`.

### `POST /api/moored/refresh` (alias `POST /api/v1/moored/refresh`)

Asks the poller to poll every moored ship's PRs now and answers `202` at once. A kick during a poll joins the next one, so polls never overlap. Admiral also polls every 2 minutes (`ADMIRAL_MOOR_POLL`).

### `POST /api/ships/<session>/dismiss` and `/unfollow`

Dismiss ends an anchored ship or a moored one. A moored ship's record goes, its name is free and one `unmoored` outcome with reason `dismissed` is written; the answer is `200 {"dismissed": "<session>"}`. `unfollow` (Don't follow reviews) ends a moored ship the same way with reason `unfollowed` and answers `200 {"unfollowed": "<session>"}`. An unknown ship, or one already ended, is `404`; a live ship that is not moored gets the `409` Dismiss gives a ship that is not anchored.

### `POST /api/ships/<session>/merge`

On top of the usual access control, on every path (local, LAN, tailnet), the request needs the header `X-Admiral-Merge-Token`, a random secret made when the server starts and served only as `<meta name="admiral-merge-token" content="…">` in the `/app/` page; a missing or wrong token, one from an earlier server process, or an `Origin` that is not the host answers `403` and runs no `gh`. This raises the bar from curling one URL to deliberately scraping the page; it is not a boundary against a local process that impersonates a browser.

The Merge act: the engineer merges a moored ship's ready PR. It runs only on this request, never from the poller, an order or an agent. The body is `{}` or `{"upTo": "<one of the ship's PR URLs>"}`; `upTo` is not Merge up to, since only a whole stack merges. Before it runs `gh`, Admiral reads the ship's PRs from GitHub again and refuses unless they are `ready` now. A ship with one PR runs `gh pr merge <url> --merge|--squash|--rebase` with the repository's default method, through the fleet's token, and no `--admin` or `--auto`. A ship with more than one PR merges as a whole stack: every PR is checked against the fleet's GitHub host and read `ready` now, then Admiral resolves the merge method, runs `gh stack view --json` in the ship's checkout (the moored record's `checkout`) and refuses unless the layers, in order, are exactly the ship's PRs: no extra layer, none missing, none without a PR, each layer's `pr.number` the number in its URL and its `head` the head commit GitHub reports for that PR. Then it runs `gh stack merge --yes --merge-method <method>` once in that checkout, with no stack or PR number, since a bare number is read as a stack number before a PR number and could merge another stack. With more than one PR, an `upTo` other than the top PR is `400` ("Merge up to is not available; the whole stack merges"); the top PR or no `upTo` merges the whole stack.

The stack's record (`<git-common-dir>/gh-stack`) and the checkout's remotes belong to the ship, so Admiral does not take them on trust: both stack calls run with `GH_HOST` and `GH_REPO` set to the fleet's host and the ship's repository, and with the checkout's `core.fsmonitor`, `core.hooksPath` and `core.sshCommand` blanked. What remains is the residual the merge token has too: it is a fence against a stray request, not a boundary against a local process, and a process that can write the checkout can still switch its branch between the view and the merge.

`202 {"merged": ["<url>", …], "method": "squash"}` lists every PR of the ship and means `gh` exited 0. A `merged` outcome is written and a poll is asked for; the ship stays moored until the poll sees the PR `MERGED`, which ends it with `unmoored` reason `merged` (a merge queue behaves the same way). Errors: `403` the merge token is missing or wrong; `404` no such ship; `409` the ship is live, is waking, has a merge under way, its fleet has no GitHub source, a PR (any layer of a stack) is not ready on GitHub now, a stack's ship has no checkout or it is gone, `gh stack view` fails, the checkout's stack is not the ship's PRs with GitHub's numbers and heads, or another live ship works in the same checkout (it could switch the branch `gh stack merge` reads); `400` `upTo` is not one of its PRs, is a lower layer of a stack, or the body is not JSON; `422` a PR (any layer) whose URL is not on the fleet's GitHub host (the argv URL is rebuilt from the parsed parts on that host); `502` GitHub or `gh` failed, the message being `gh`'s stderr, and the ship stays moored; `504` `gh` ran past 2 minutes, so the merge's result is unknown, and a poll is asked for since it may have happened.

### `POST /api/ships/<session>/wake` (alias `POST /api/v1/ships/<session>/wake`)

Wakes a moored ship by hand: the same ship, under the same name, starts a feedback round. Body: `{"text": "<optional>"}`. The prompt is the report's `resume` text as it is, then a space and `text` when there is any; no skill is prefixed again. The wake is a hand launch: it is not autopilot, counts toward `max_ships` and is never blocked by it, and it writes no feedback file. The round number is the record's `round` plus one. The ship keeps its fleet, harness, model, skill, checkout and resources; crew pins do not carry over. Answers `201 {"ship": {...}}`, the shape `POST /api/ships` answers. Errors: `400` for a body that is not valid JSON; `404` for an unknown or ended ship; `409` for a live ship, for a wake of the same ship already in progress, for a moored ship whose report gave no valid `resume` text (it must be `/skill resume <run>` or `$skill resume <run>`, nothing more), for a ship ended (Dismiss, Don't follow reviews, a merged PR) while its wake waited, and for a resource the ship holds that is busy; `503` while Admiral has not yet read its tmux sessions. A Dismiss that arrives once the wake is making the session is the one that loses: it answers `404`.

### A stranger's proposal: `GET /api/inbox`, `POST /api/inbox/_admiral/<id>/launch` and `/dismiss` (also under `/api/v1`)

A stranger's feedback on a moored ship's PR is an Inbox item with `kind: "wake_proposal"`, `session: "_admiral"`, `name: "Admiral"` and a title `Wake <ship> for @<login>'s review?`. Its `wake_proposal` object is `{"ship": "<name>", "session": "<session>", "pr": "<url>", "login": "<login or ghost>", "events": [{"id", "kind", "url", "at", "body"}]}`. Each `body` is the event's text as GitHub gave it, cut to 4096 bytes; show it as plain text. A proposal holds one batch and is never rewritten; a later comment makes a new one.

`launch` takes `{}` or `{"events": ["<id>", …]}`, the event IDs the engineer was shown: a list that differs from the item's is `409`. It starts a feedback round for the PR, as autopilot, handing the ship the proposal's events in `.admiral/untrusted/feedback-<N>.md`. It is never blocked by `max_ships`. Answers `201 {"ship": {...}}` and writes the answer `{"launched": true, "ship": "<name>"}`. Errors: `404` for an unknown item, a ship no longer moored or an item that does not match the moored ship and what the poll proposed; `409` for an item already answered, a live ship, a wake in progress or a ship with no valid `resume` (the item stays); the rest as `POST /api/ships/<session>/wake`. `dismiss` takes `{}`, writes `{"dismissed": true}` and answers `204`; a second press is `409`. Remove from inbox on a proposal dismisses it. Neither brings the comment back.

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

## Sending to a ship

`GET /api/version` lists the `ship-input` capability when the server has these routes. They are unversioned in the code; `/api/v1/...` reaches them too.

- `POST /api/ships/<session>/messages` with `{"text": "..."}` pastes the text into the ship's pane as one bracketed paste and presses Enter, then clears needs you as `input-seen` does. The text is cleaned first: CRLF and lone CR become LF, control characters other than LF and tab are removed, and the edges are trimmed. Response `{"sent": true}`.
- `POST /api/ships/<session>/crew/<name>/messages`: the same for a crew boat, which clears the boat's needs you.
- `POST /api/ships/<session>/interrupt` and `.../crew/<name>/interrupt` press Escape in the pane. They don't clear needs you. Response `{"interrupted": true}`.
- `POST /api/ships/<session>/stop` ends a ship, live or anchored, and its crew boats with it. A live ship's tmux session is killed and the outcome log gets `ended` (signal `KILL`, no exit code) then `stopped`; an anchored ship gets the `ended` it already has, then `stopped`. The ship leaves the list. Response `{"stopped": "<session>"}`.
- `POST /api/ships/<session>/answer` and `.../crew/<name>/answer` answer the permission or question menu the pane shows, by sending the harness's keys (`internal/keymap`: Allow once is `1`, Deny is Esc, a question's option is its digit; Copilot's keys are `1`, Esc, and Down then Enter, since its question form ignores digits). The body is `{"card":"permission","reason":"permission:<Tool>","choice":"allow"|"deny"}` or `{"card":"question","reason":"<the pane's reason>","item":"<question item id>","option":<1-based number>}`. Answers go one at a time per pane, and the gate reads the pane's own `@admiral-state` and `@admiral-reason`, not the poll cache: keys are sent only while the pane needs you and its reason equals the body's `reason`. Claude Code also fires PermissionRequest for AskUserQuestion, so a question's pane reason is `question` or `permission:AskUserQuestion`, and the question card accepts either. The permission card refuses `permission:AskUserQuestion` and `permission:ExitPlanMode`, whose menus are not yes or no, and a Codex tool other than `Bash` and `apply_patch`, whose menus are unrecorded. A question also needs `item` to be the newest pending question of the pane's conversation, alone in its call (a call with several questions is answered in the terminal), not multi-select, with an option `option`. Allow and a question answer clear needs you as `messages` does; Deny leaves the pane needing you with reason `stop`, because the harness sits at its prompt: no hook says so for Claude or Copilot, and Codex's `Interrupt` hook writes the same `stop` after the route does. A Copilot pane is answered in place too: its permission card takes `notification:permission_prompt` and its question card takes `question` or `notification:elicitation_dialog`, each for a pending single-choice `ask_user` of 1-9 options (a several-field form, and a crew boat before its first `agentStop`, go to the terminal). Response `{"answered": true}`. `400`: invalid JSON, an unknown card or choice, or a missing field, whatever the pane shows. `404`: unknown ship or boat. `409` and no keys sent: the pane is not on that card now, has exited, or runs a harness with no key map, or the question is not answerable as above. `502`: tmux refused the keys.
- `400`: the message is invalid JSON, empty after cleaning, or over 16 KiB; the body is capped at 1 MiB. `404`: unknown ship or boat. `409`: the ship or boat is anchored or sailing off (send and interrupt). `502`: tmux refused the keys. Stop answers `500` when the session cannot be killed.

## A ship's conversation

The conversation of a Claude Code, Codex or Copilot ship, read from the harness's own record (Claude Code's transcript, Codex's rollout, Copilot's `events.jsonl`). `GET /api/version` lists the `conversation` capability when the server has it.

### How the file is found

The launcher starts a Claude Code ship with `--session-id <uuid>` and sets the pane option `@admiral-transcript` to `<unix ns> launch <path>` before the harness runs. `<path>` is `<config dir>/projects/<project slug>/<uuid>.jsonl`, where the config dir is the one Claude Code uses for the fleet's Claude dock (`CLAUDE_CONFIG_DIR`, else `~/.claude`). Claude Code may store the file under another project directory name (it shortens long ones), so when the computed path is missing the server looks for `<config dir>/projects/*/<uuid>.jsonl`.

A dock whose args, or an order whose extra args, already choose a session (`--session-id`, `--resume`, `-r`, `--continue`, `-c`) launches as before with no `--session-id` of ours and no option. That ship's state is `none`.

The option's text is untrusted: anything running in the ship can write it. The session id in the option must equal the `--session-id` in the command the pane was started with (tmux keeps that, and `set-option` from the ship cannot change it; only a respawn of the pane, which replaces the harness too, can); on a mismatch, or when the start command has no `--session-id`, the state is `none`. The server takes the file from the option's path, else from the `projects/*/<uuid>.jsonl` lookup above, and reads it only if it is a regular file under the harness's own `<config dir>/projects/` with no symlink on the way (ADR 0019). A file or directory that is a symlink is `unreadable`, never `ok`.

A Copilot ship launches with `--session-id <uuid>` and records `<copilot home>/session-state/<uuid>/events.jsonl`; the server checks the id against the start command the same way, and the file's layout is fixed by the id, so a missing file is not searched for. When Copilot accepts `--ui-server` the launch also passes `--ui-server --port <n>` on a free loopback port. The server reads `<n>` back from the start command (never from a pane option) and follows that Copilot's live events while a client has the ship open; see "Live text" below. A ship whose command has no `--ui-server` is served from the file alone. When the order or the dock already carries `--server`, `--headless`, `--stdio`, `--connect`, `--host` or `--port`, the launcher adds nothing; but if a dock passes `--ui-server --port N` itself, Admiral still observes N, subject to the ownership check (the listener must be the pane's process or one of its descendants).

A Codex ship has no `--session-id` to launch with. Its `SessionStart` hook writes `@admiral-transcript` as `<unix ns> <source> <path>` from the payload's `transcript_path`: the rollout, `<Codex home>/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`, where the Codex home is the fleet's `CODEX_HOME`, else Admiral's, else `~/.codex`. A null `transcript_path` records nothing, so the state is `none`. The server takes any source word, since a pane can write the option and nothing binds a Codex ship's path to its launch, and reads the file only if it is a regular file under `<Codex home>/sessions/` with no symlink below it (a symlinked home or `sessions/` is fine). A pane can therefore point the option at another rollout under the same `sessions/`. `SessionStart` fires again on resume, `/new`, fork and compact and records the new path: the server reads the option again about once a second and switches to the new file. A rollout has no pending question. The adapter reads a `compacted` record as a compaction marker, a shape no real rollout has confirmed.

### Live from the app-server (Codex)

A running Codex ship's server (`codex app-server`, see the codebase notes) also tells its conversation, and Admiral keeps one observer per running ship, shared by every view. While the observer is connected it is the only source: `GET` and the WebSocket `conversation` messages carry the server's items, and the rollout is not read. A ship with no rollout yet still has a conversation then.

The observer connects to the pane's `@admiral-codex-socket` only when the pane's start command names the same `--socket`, and only if the socket resolves (symlinks followed) to a socket of this user in `/tmp/codex-daemon-<uid>`. It sends `initialize` (with `experimentalApi`), `initialized`, `thread/loaded/list`, `thread/resume {threadId}` and, when several threads are loaded and none matches the rollout, `thread/list {limit: 50}`, nothing else, and never answers a request of the server: the TUI answers, and the observer would be deciding for the engineer. Frames are read with no timeout; a connect or a handshake that takes longer than the bound fails.

The thread is the one the server last announced with a non-ephemeral `thread/started` that is not a sub-agent's (the TUI's `/new`, `/resume` or a fork), else the one whose id is in the `@admiral-transcript` rollout's file name when the server has it loaded, else the only one loaded, else the most recently updated of those loaded. The history on attach is the `thread/resume` answer. Items: a user message, a finished agent message, a command as `Bash` with the full command in `input`, a file change as `apply_patch` with its paths one per line in `input`, a compaction, and a turn end. Reasoning and hooks are not shown.

The observer follows the TUI: a `thread/started` for another thread, or a new `@admiral-transcript` rollout, makes it attach to that thread at once, and each open view gets `reset: true` with that thread's history. A running ship's pane options and rollout are read when its panes change and otherwise every few seconds, so a change of rollout shows within that time; `thread/started` does not wait for it.

A live view holds the last 200 items of the thread and `GET`'s `more` and `before` page inside them; the rollout view has the rest. The observer does not page older items from the rollout, so that a view has one source between resets.

A tool call from the server carries `live: true`. A command or file change the server asks to approve carries `approving: true`, with `input` and `summary` from the request's own command and cwd. Only one plain approval waiting (a `command` kind with no `approvalId`, or a file change with no `grantRoot`) is marked; any other approval, or more than one, is marked on nothing and the card sends the engineer to the terminal. An approval clears on `serverRequest/resolved` for its request and at turn end. A question key for a ship with an approval waiting is refused with `409`.

A Codex question is answered the same way, with `reason` `bell` and Codex's keys, while the observer holds a pending single-choice question that is the newest one, alone in its call, with no approval waiting. Codex's TUI rings no bell for `request_user_input` in a pane it takes as focused, and no hook fires for one, so the observer's pending question is the signal: it sets the pane to `needs_you` with reason `bell` when a question starts waiting (unless a hook already has the pane at `needs_you` for something else), and back to `working` when the request resolves or the thread changes, clearing only its own `bell` mark. A real bell sets the same state. The observer sends nothing over the protocol.

A question already pending when the observer attaches is not shown as a card: the server's `thread/resume` answer holds the history, and the request itself is not replayed to the observer, so the engineer answers it in the terminal. (Not verified against a 0.167.1 server beyond the recorded probe, where the observer received requests only after it had attached.)

A view that opens while the observer is connected gets no `reset`. When the connection drops, each open view gets `reset: true` with the rollout's items (when the ship has one), with `reset: true` and no items when there is no rollout either, and when the observer reconnects, `reset: true` with the server's history. The observer retries with a growing pause. A pane whose option and start command disagree is never dialled and its conversation is the rollout's.

A pending question is a `question` item whose `id` is `<item id>#<index>`. Only a request with one question, with options and not secret, is answerable from the card; Codex rings the bell for it, so the pane's reason is `bell` and the answer route takes `{"card":"question","reason":"bell",...}` only while the observer holds a pending question whose id is the request's `item`. Any other state is a 409.

### `GET /api/v1/ships/<ship>/conversation[?before=<id>]`

```json
{"state": "ok", "items": [ ... ], "more": true}
```

- `state`:
  - `ok`: the file was read. `items` may be empty (a new session has only metadata lines).
  - `none`: the ship has no conversation Admiral can read: not a Claude Code, Codex or Copilot ship, no `@admiral-transcript` of ours, or the file has not been written yet.
  - `unreadable`: the path is refused (a symlink, not a regular file) or nothing in the file parses.
  - `gone`: the file was read before and is not there now. This is kept in memory, so after an Admiral restart a missing file is `none` until it is seen again.
- `items`: at most 200, oldest first, the newest 200 without `before`. An item is the model in `internal/conversation` (`id`, `kind`, `text`, and the tool call or question it holds; a tool call carries `name`, `summary`, `output`, `error`, `done` and `input`, what allowing it approves: a Bash call's full `command`, or the path of a Write, Edit, MultiEdit, NotebookEdit or Read call (on Copilot, the `command` of `bash` or the path of `edit`, `create` or `view`), cut at 4 KiB with a marker and omitted for any other tool, while `summary` stays the one-line label, which may be the model's own description; a question carries `text`, `options`, `answer`, `pending` and `multiSelect`, present and true for a question that takes several answers). Ids contain characters that need URL escaping in `before`.
- `before`: the id of an item; the page is the up to 200 items before it. An empty `before`, one that is not an item's id, or one older than the window the server reads is `400`.
- `more`: there are items older than the first one returned, within the window the server reads. It is `false` at the file's first item.
- An unknown ship is `404`. A dismissed ship is unknown. A ship anchored after its harness exited stays readable until it is dismissed.

### A ship's rounds

A moored ship is relaunched under the same name for each feedback round, and each round is a fresh session. Admiral records each round when the ship moors: its session id, fleet, transcript path, launch time and the time it moored, in `$ADMIRAL_HOME/moored/rounds/<ship name>.json` (paths only, never content; the history keeps the latest 50 rounds, and an older round's divider and items drop off). A round's path is recorded only when it passes the same check as above (the option's session id equals the `--session-id` of the start command); any other round, a non-Claude one included, is recorded with no path. The history ends with the mooring (Dismiss, Don't follow reviews, merged or closed), and a wake keeps it.

When a ship has rounds, the endpoint stitches them oldest first:

- **A moored ship** is served from its history, with state `ok`, where it has no live session and would otherwise be `404`. A moored ship whose page would have no items (no history, a history that cannot be read, or only an undivided round 0) answers `none`.
- **A live ship in round N of 1 or more** serves the recorded rounds, then its live round.
- **A `round` item opens each round of 1 or more**: `{"id": "r<N>", "kind": "round", "round": N, "at": "<the round's launch time>"}`. It carries `state` when the round cannot be shown: `none` (no transcript recorded, for example a non-Claude round), `gone` (the file is no longer there) or `unreadable` (the file cannot be read or parsed). Such a round shows only its divider, and never fails the page. A live round's divider has no `state` while its transcript is not bound yet (right after a wake) or its file is not there yet. Round 0 has no divider, so a missing round-0 file shows nothing.
- **Ids:** an item of round N of 1 or more has the id `r<N>:<id>`, on the page and on pushes alike. Round 0 keeps its own ids.
- **`before`:** an id of the form `r<N>:<id>` for a round N of 1 or more in the history is read in that round, and `r<N>` (the divider) pages from the round before. Any other id is round 0's.
- **Paging** reads the newest round first and opens an older file only while the page has room, at most 8 files a page; `more` is true while anything older exists, so a page may hold fewer than 200 items.
- `tail` comes from the live round.

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

- `reset: true` is added when the transcript was truncated or replaced, or when a Codex ship's recorded rollout changed to another file: `items` are what the file now holds, and the client drops what it holds.

- A changed item is sent again under the same `id` (a tool call whose output came later); the client replaces its copy.
- A message holds at most 200 items.
- An open for an unknown ship, or one with no conversation, is ignored, with no reply. An open for a moored ship is answered with `conversation_opened` and watches nothing: its rounds are recorded, and its file no longer grows.
- A live round's pushes carry the same `r<N>:` ids as its page.
- A client holds at most 16 ships open. A 17th open closes the oldest.
- A subscription ends on `conversation_close`, when the connection closes, or when the ship is dismissed.
- The server follows the file by offset, about four times a second while any client has the ship open.
- Live text (Copilot ships with a `--ui-server`): while a client has the ship open, the server also attaches to the ship's server as a read-only second client and pushes the assistant text of a turn as it is written, before the file has it. A live reply has the id of Copilot's message (`messageId`), which is also the id the file's item gets, so the finished item replaces the live one and the text never shows twice. Every other live item has the id of its event, which is the id of its line in the file. The HTTP page and the ship's `tail` read the file only. An interrupted reply (Esc, an error) keeps the partial text it already showed in every open tab until the conversation is reopened, because there is no message that removes an item; the file page never has it. Subagent text is not shown live, as in the file. When the server cannot be reached, is not the ship's own, or goes away, the conversation comes from the file alone, with no error; the server retries slowly.

## Inbox

The items ships leave for you: a question, a review, findings, and Admiral's own notices. Every route is also served under `/api/v1/`.

### `GET /api/inbox` (alias `GET /api/v1/inbox`)

```json
{"items": [{"version": 1, "id": "q-1", "kind": "question", "title": "Which port?", "created": "2026-10-22T10:00:00Z",
  "session": "s1", "name": "Dory", "harness": "claude", "fleet": "work", "fleet_name": "Work", "fleet_color": "#38bdf8",
  "question": {"questions": []}}]}
```

Oldest `created` first. An item is its file's fields plus `session`, and `name`, `harness`, `fleet`, `fleet_name` and `fleet_color` when the ship is known, live or from the record Admiral kept after it sailed off. Admiral sets those last, so a file cannot name another ship or fleet; they are omitted for an unknown ship and on a notice (session `_admiral`, name `Admiral`). `kind` is `question`, `review`, `findings` or `notice`, and the item holds only that kind's own object (`question`, `review`, `findings` or `notice`). The WebSocket `inbox` message, `{"type": "inbox", "items": [...]}`, carries the same list and is sent to each new connection and on every change.

### Acting on an item

Each route is `POST /api/v1/inbox/<session>/<id>/<action>` and answers `404` for an unknown item. An item already answered is `409` and final. A `409` is also returned while another action on the item is running (`another action on this item is still running`); that one can be retried.

- `answer`: a question's answers, `{"answers": {"<question id>": {"choice", "other"}}, "note"}`; `400` for an invalid answer.
- `submit`: a review, `{"event": "COMMENT"|"APPROVE"|"REQUEST_CHANGES", "edited": bool}`, posted to GitHub through `gh`.
- `discard`: a review, `{}`; deletes the pending review on GitHub. `{"forget": true}` runs no `gh`.
- `findings`: `{"cards": [{"id", "decision": "approved"|"edited"|"rejected", "proposal": {"kind": "diff"|"reply", "content"}}]}`; every card needs one decision and only an edited card takes a proposal. Nothing is posted to GitHub.
- `remove`: Remove from inbox, any item; changes nothing on GitHub and never resumes the ship. Answers `200` with `"forgotten": true`. A notice's file is deleted.

## Crew

A fleet's Crew page: its built-in and fleet members, their notes, and the edits the page makes. Every route is also served under `/api/v1/`, and `{key}` is a fleet key (an unknown fleet is 404). Only the **built-in** and **fleet** layers are listed; repo-layer and ad hoc members are not. Error bodies are `{"error": "<message>"}`. A 400 that names a member field also carries `"field": "<key>"` (`role`, `based-on`, `skills`, `model`, `harness`, `pick-me-when` or `instructions`). A refusal changes nothing on disk, and no write follows a link: a link anywhere from the crew directory down to the file is refused with 409.

### `GET /api/fleets/{key}/crew[?harness=<dock>]`

`{harness, members, unclaimed, warnings, errors}`. The built-in layer is the Commodore installed for the dock `harness`, which defaults to the fleet's first dock (`""` when it has none, and then the built-in layer is empty); an unknown dock is 400. A missing crew directory reads as empty and a GET never creates it.

A member is `{name, role, based-on, skills, model, harness, pick-me-when, instructions, layer, customized, notes}`. `layer` is `built-in` or `fleet`, the winning layer; `customized` is true for a fleet member with a built-in member of the same name. `notes` is `{fleet: [...], repos: {"<owner>/<repo>": [...]}}`: strings without the leading dash and space, only scopes holding a note, the fix checker's always empty. Only the first 30 notes of a scope show, so lines past 30 in an over-full file can't be reached from the page. A link, a non-directory or an `<owner>` or `<repo>.md` name that is no path segment is skipped with a warning.

`unclaimed` is `[{member, notes}]` sorted by name: every `memory/<name>/` holding a note whose name no built-in or fleet member uses. A repo-layer member's notes show here too, and the set can differ by `?harness=` when the docks' Commodore installs differ. A directory name that is not a member name is skipped with a warning. When the resolver reports `errors`, `members` and `unclaimed` are empty.

### Fleet members

Edits touch only `$ADMIRAL_HOME/fleets/<key>/crew/`. A member route keys on whether `<name>.md` exists there, so a PUT can repair a broken file. A name with no fleet file but a built-in member is 409 (customize it first); with neither it is 404; a name that is no member name is 400. Each success is 200 `{"name": "<name>"}`. The built-in layer is the `?harness=` dock's, as in GET.

- `PUT /api/fleets/{key}/crew/members/{name}` with `{role, based-on, skills, model, harness, pick-me-when, instructions}` rewrites the file (Go writes the frontmatter and the instructions as the body). It is 400 with `field` when the resolver would refuse the bytes (a parse or field error, a custom role without a valid `based-on`, a built-in role with `based-on`), when `role` or `based-on` differs from the same-named built-in, when a value is not one line, or when a value would read back differently (a skill name that is not valid, instructions with leading or trailing blank lines, the fix checker's skills and instructions).
- `POST .../members/{name}/customize` copies the built-in member's file into the fleet layer: 404 with no built-in of that name, 409 when the fleet already has it. Its notes stay with the name.
- `POST .../members/{name}/rename` with `{to, carry_notes}` renames a fleet member: `to` is a member name that differs from `name` (400) and names no listed member (409). With `carry_notes: true` its `memory/<name>/` moves to `memory/<to>/`, and the whole rename is 409 if `<to>` already has a note. Otherwise the notes stay under the old name, and show in `unclaimed` unless a built-in member of that name claims them.
- `DELETE .../members/{name}` removes the fleet file. Its notes stay; a customized built-in falls back to the built-in member.

### `POST /api/fleets/{key}/crew/notes`

`{op, member, scope, repo, note, text, to}`: `op` is `edit`, `move` or `delete`; `scope` is `fleet` or `repo` (with `repo` as `<owner>/<repo>`); `note` is the note exactly as GET returned it. `member` need not resolve, so an unclaimed name works. A `member` or `repo` that is not a valid path part is 400. The note is the first line equal to `- <note>` (a trailing `\r` ignored); none is 409, and the page reloads. Every other line of the file is kept byte for byte.

- `edit` replaces the fact and keeps the date and run. `text` is one line of visible text, at most 500 bytes, and the line must pass the resolver's note check (400).
- `move` takes `to: {scope, repo}`, removes the line and appends it unchanged to the destination, making `memory/<member>/repos/<owner>/` as needed. A destination equal to the source is 400; one that already holds 30 notes is 409.
- `delete` removes the line. An empty scope is not listed.

The response is 200 with the member's notes after the change, in GET's `notes` shape.

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

What the launcher puts in the prompt: the `skill` runs with the text as its argument (`/skill text`, `$skill text` on Codex). The text is the child's `skill.args` when set (`<map URL> <ticket URL>` for `/wayfinder`), else the ticket URL; a child with no skill gets its title and URL. A slice's text is its children's URLs, one per line.

A slice: `issues: ["<issue URL>", ...]` in place of `child_url` (never both, `400`) launches two or more children as one Commodore ship. `start_anyway` is not read. Every child must be ready, resolved to Without you and to the `commodore` skill kind (an override counts), and all in one repo. Refusals, naming the child where one is at fault: `400` for fewer than two issues, a URL that is not an issue URL, a URL containing a comma, an issue listed twice (once normalised by the claim key), a parent or children in two repos; `404` for an issue not in the campaign; `409` for a child that is done, blocked (listing its blockers), underway or assigned with no ship, With you, on another skill or not ready, and for any child a live ship already holds, which includes the same set launched again in any order (the message names the ship; while two such launches race it says "a launch in flight"). The ship has `autopilot` off, `issues` in the order given and a title of the first child's title and `+N`. Its `order_id` is `<campaign id>/<first issue URL>,<next>,...`, the children in launch order, where a child in the first one's repo is written as its issue number alone (`c0ffee/https://github.com/acme/widgets/issues/31,32`); that id answers `/api/launch-orders/dirs` and the clone, and the issue URLs are recovered from it. A single child's `order_id` is unchanged. The text is the child URLs, one per line, built from the URLs alone.

A campaign ends when the parent and every issue under it, nested parents included, are closed, and none has sub-issues that could not be read; or when it is unfollowed. It is reread when a child's ship anchors or sails off, when its page is opened, and every five minutes.

## A crew boat's conversation

Each boat in a ship's `crew` list carries `role` (the `--role` it was launched with, left out when none) and `conversation` (always present: `true` only for a Claude Code, Codex or Copilot boat with a recorded transcript). The recorded transcript path itself is not on the API.

### `GET /api/v1/ships/<ship>/crew/<name>/conversation[?before=<id>]`

The same body, states and paging as a ship's conversation. The file is the transcript path the boat's hooks recorded (ADR 0021), read fresh from its pane on each request. A Claude Code, Codex or Copilot boat with no path recorded yet (a Copilot boat's is recorded at its first `agentStop`) reads `none`. An anchored (exited) boat stays readable until it is closed. An unknown ship or boat is `404`, and so is a boat that was closed.

A boat has no `--session-id` of ours to check the path against, and a boat writes the path itself, so the only limit is the one every transcript read has (ADR 0019): the file must be a regular file under the boat's fleet's `<config dir>/projects/` (Copilot: `<copilot home>/session-state/<id>/events.jsonl`; Codex: `<Codex home>/sessions/`) with no symlink on the way, else `unreadable`. A boat may therefore show any transcript in its fleet's `projects/` or `session-state/`, including another session's. Binding a boat to its own session is follow-up issue #357.

### WebSocket

The ship's messages take an optional `crew` field naming the boat: `{"type": "conversation_open", "ship": "<ship>", "crew": "<name>"}` and `conversation_close` the same way. The server echoes `crew` on `conversation_opened` and on every `conversation` push for that boat; the ship's own messages have no `crew`. A boat counts toward the 16 open conversations, and its subscription ends with `conversation_close`, when the connection closes, or when the boat's window closes or the ship is dismissed.

## A ship's changes

`GET /api/version` lists the `changes` capability when the server has it.

### `GET /api/v1/ships/<ship>/changes`

```json
{"state": "ok", "files": [{"path": "a.go", "status": "modified", "added": 3, "removed": 1}], "diff": "diff --git ...", "truncated": false}
```

Read-only: the ship's checkout (its `dir`; a moored ship's recorded checkout) against its merge-base with the base branch, committed, staged, unstaged and untracked work together.

- The base branch is the repo's `origin/HEAD` branch, else `main` (`refs/heads/main`, then `origin/main`). With none of these, or when `git merge-base` fails (unrelated history, a shallow clone), the base is `HEAD`, so only uncommitted work shows.
- `state` is `ok`, or `none` (with `files: []` and `diff: ""`) when no `.git` is found at `dir` or above it, `dir` is gone, or the repo has no commit yet. A ship in a subdirectory gets the whole repo's changes, with repo-relative paths. The work tree is the directory holding `.git`, whatever `core.worktree` says.
- `status` is `added`, `modified`, `deleted`, `renamed` (`path` is the new name), `copied`, `typechange`, `unmerged` or `untracked`. `added` and `removed` count lines, `0` for a binary file.
- `diff` is the unified diff of the tracked files, then one for each untracked file as a new file. It is cut at a line end after 2 MiB, and the list stops at 2000 files; `truncated` is `true` when either happened, and also when the 20 s budget runs short before every untracked file's diff is in (the list still has them).
- An unknown ship is `404`. A git failure or a timeout (10 s a call, 20 s in all) is `500`.

The checkout's `.git/config`, `.git/info/attributes` and `.gitattributes` are written by the ship, so git runs with `--no-ext-diff --no-textconv`, `core.fsmonitor` and hooks off, every `filter.*` and `diff.*` program named in the repo config (included files and `config.worktree` too) blanked, no optional locks and a fixed environment (no global or system config), and no user-controlled value reaches it as an option.

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

## Devices and pairing

A device is a browser or client that holds its own revocable token. A request from this Mac needs no token. Any other request needs the device's token, as `Authorization: Bearer <token>` or the cookie `admiral_token_<port>`; `?token=` is not accepted. The cookie is HttpOnly and SameSite=Strict, so a request that carries it must also pass the Origin check.

### `POST /api/v1/pairing-codes`

Behind the same auth as the other routes. Answers `{"code":"ABCD-EFGH","url":"http://<host>:<port>/pair?code=ABCD-EFGH","expires":"<RFC 3339>"}`. The code lasts 5 minutes and works once, and a new code replaces the live one.

### `POST /api/v1/pair`

Body `{"code","name"?}`. A code is case-insensitive, with or without the hyphen. On success it answers `{"token","device":{"id","name"}}` and sets no cookie, so a client sends the token as a Bearer; `name` defaults to a guess from the browser, such as `Safari on Mac`. A wrong, expired or used code is 400; ten failures in 5 minutes are 429 with `Retry-After`; a request from this Mac is 403. `GET /pair?code=<code>` and the form at `GET /pair` (which posts to `POST /pair`) pair a browser instead: they set the device cookie and redirect to `/`.

### `GET /api/v1/devices`

Answers `{"devices":[{"id","name","login"?,"pairedAt","lastSeen","current"}]}`. `login` is set for a device paired over the tailnet. `current` is true for the device making the request.

### `PATCH /api/v1/devices/<id>`

Body `{"name"}`. Answers `{"id","name"}`; 400 for a missing or empty name, 404 for an unknown id.

### `GET /api/remote` (alias `GET /api/v1/remote`)

Behind the same auth as the other routes. Answers `{"tailscale":bool,"allowedLogins":[…],"url":"","serveOff":"","listenExplicit":bool,"checks":[{"id","ok","detail"}]}`. `tailscale` is the mode and `allowedLogins` the tailnet logins allowed in. `listenExplicit` is true when `--listen` fixed Admiral's address, so the mode never moves the listener. `serveOff` is `tailscale serve --https=<port> off` for the HTTPS port serve proxies to Admiral on (read from `serve status`, so 443 or a hand-set 8443), empty with the mode off or no such handler. With the mode off `url` is empty, `checks` is `[]` and no `tailscale` command runs. With it on, each call runs the tailscale CLI (4 s limit per command) and answers four checks in order: `running` (tailscaled up and logged in), `https-magicdns`, `serve` (an HTTPS handler proxying to Admiral's loopback port; a raw TCP forward to that port fails it, since it bypasses Admiral's tailnet checks) and `funnel-off` (Funnel on for an HTTPS handler or a TCP forward to that port, in serve or a foreground serve; the detail names the command that turns it off). `detail` is one line. `url` is `https://<MagicDNS name>[:<port>]/` when serve has such a handler and the node is logged in, else empty. A check that can't be answered (tailscale missing, down, slow or unparsable) is `ok:false` with the reason. When the last move of the listener failed, a fifth check `listen` (`ok:false`, the bind error as `detail`) is added in either mode; the old listener is still in place, and the next successful move clears it.

### `POST /api/remote/logins` (alias `POST /api/v1/remote/logins`)

Body `{"add": login}` or `{"remove": login}`, one of them. The login is trimmed. Answers 200 with the same view as `GET /api/remote`, read after the write. 400 for a blank or already-allowed login to add, for neither or both keys, and for a body that isn't JSON; removing a login that isn't listed changes nothing. The write goes through the config manager (`Manager.SetRemote`) under one lock, so a removal keeps the other logins and edits made at once all land. The config change is what cuts the removed login's open tailnet sockets, just after the write returns; this route cuts nothing itself. A tailnet device may edit, including its own login.

### `POST /api/remote/setup` (alias `POST /api/v1/remote/setup`)

Body `{"confirm":true}`; anything else is 400 and nothing runs. Only a request from this Mac may run it: any other path gets 403 and nothing runs. It runs the same checks as `admiral setup` (running, HTTPS and MagicDNS, Funnel off), then, unless serve already proxies to Admiral's port (left as it is), `tailscale serve --bg --https=443 http://127.0.0.1:<Admiral's port>` under the serve timeout, then writes `remote.tailscale: true` and adds the Mac's login to `allowedLogins`. Answers 200 `{"ok":bool,"checks":[{"id","ok","detail"}]}`: one row per check up to the first failed one, then `serve` (passed, left as it is, or failed with the CLI's message) or `https-443` (failed: another handler holds 443; the detail has the `--https=8443` command). A failed row stops it before anything is written. 409 while another Set up runs, so a double click runs serve once. The mode applies live: the config change moves the listener (see the move message below).

### `POST /api/remote/off` (alias `POST /api/v1/remote/off`)

Turn off. Body `{"confirm":true}`; anything else is 400. Only a request from this Mac may run it: any other path gets 403 and nothing runs. It writes `remote.tailscale: false` and nothing else: the allowed logins stay and `tailscale serve` is left as it is. Answers 200 with the `GET /api/remote` view after the write, except that `serveOff` is the command read just before the write, so the page can keep showing it. 500 `{"error"}` when the write fails, without the path.

### The move message

When a config change moves Admiral's listener, a ships-socket client whose path stops working gets `{"type":"move","url":"<address>"}` before it is cut. Mode on: clients that came in over the LAN get the tailnet URL (`GET /api/remote`'s `url`, empty when serve isn't set up). Mode off: clients that came in over the tailnet get the LAN address (`http://<ip>:<port>`). Loopback clients never get it. The server waits up to 2 s for the writes, then cuts the tailnet sockets the change no longer allows, then rebinds; with the mode on, every LAN socket still open is then closed (a client that ignores the message loses its socket). The shell sends the page to `url` when it is an http(s) address other than the page's own; an empty `url` leaves the page where it is. A device that moves to the tailnet pairs again there, since its token and cookie belong to the old origin.

### `DELETE /api/v1/devices/<id>` and `DELETE /api/v1/devices`

Revoke one device or all of them; 204, or 404 for an unknown id. The device's token stops working and its open WebSockets close. A save that fails leaves the device in place and answers 500 `{"error"}`, as it does for a rename.
