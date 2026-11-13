# API

The HTTP and WebSocket shapes a client reads. Every route is also served under `/api/v1/...` and `/ws/v1/...` (see "API versions" in `docs/agents/codebase.md`). The bridge sockets are in `docs/bridge-protocol.md`.

## Ships

### `GET /api/ships` (alias `GET /api/v1/ships`)

`{"ships": [...], "home": "..."}`: the ships, and the id of the home they belong to. Each ship is also in the WebSocket `{"type":"ships","ships":[...]}` message, which is sent on connect and again whenever any ship's fields change, its `tail` included.

A ship AdmiralBot launched also carries `via` (`"admiralbot"`); the engineer's ships omit it. A request that carries an actor token sees only its fleet's ships (see "AdmiralBot's requests").

A ship's `url` is its issue. A ship launched without an order gets its `title` and `link` once its first prompt is known, whether it came with the launch or was typed later: `link` is the main link of the prompt (the only one, or the one the titler picked), and `title` is, in order, the GitHub title of `url` or `link` when it is a pull request or issue on one of the fleet's GitHub hosts, Claude Code's own title for the session, the titler's, or the prompt's first line without its links. `titling` is true while that is worked out and omitted otherwise. Unlike `url`, `link` claims nothing. The title and link are set once: an order's ship and a later round keep the ones they launched with. A ship that holds several issues (a slice) also carries `issues`, every issue URL in order with `url` the first; a single-issue ship omits `issues`. The moored record keeps the list, so a moored slice ship carries it too.

A live ship also carries `open_prs`: the PRs it carries that the last poll saw `OPEN` (see `carries_prs` below), then the PRs in its report that GitHub confirms open on the fleet's host and routed to its fleet, each URL once; omitted when there are none. The report half is checked as below; the carried half is read from the poll's state, so it adds no GitHub call here. The server checks GitHub only when the report file's modification time or size changes, never while serving this request, so a ship that just wrote its report shows `open_prs` a moment later. A check GitHub does not answer is retried later with a growing wait (30 s, doubling to 4 min), the report unchanged. When the ship's turn ends (needs you, reason `stop`) and its report's `result` is `done` with at least one open PR, and it is not returning, the server moors it without a request (only a `done` report triggers this, whatever the ship carries): it appears as `moored` with its `prs`, and the outcome log gets `moored`, then `ended` (signal `KILL`).

A moored ship has `status` `moored` and no tmux session: it is listed from its record in `$ADMIRAL_HOME/moored/`, with `prs`, `moored_at` (RFC 3339), `polled_at`, `stale`, `no_resume` and `waiting`, which no other ship carries. `waiting` is a string, present while a feedback round is due and has not started: `Waiting for room under the cap`, `Waiting for <resource> (busy|missing|pending)`, `Waiting for the current wake to finish`, `Waiting for Admiral's terminal host`, or `Can't wake: <the refusal>` for one that a later poll won't clear. It clears when a poll answers and does not decide to wake, on a successful wake and when the mooring ends; a poll that fails to reach GitHub keeps it. `prs` is a list of `{url, state, review, checks, mergeable, conflicts, ready}`: `state` is `OPEN`, `MERGED` or `CLOSED` (a PR GitHub answers NOT_FOUND for counts as `CLOSED`; a NOT_FOUND on its repository says nothing about it, so its ship stays moored and `stale`); `review` is GitHub's `reviewDecision` (`APPROVED`, `CHANGES_REQUESTED`, `REVIEW_REQUIRED`, or `""` for none); `checks` is the status check rollup (`SUCCESS`, `FAILURE`, `ERROR`, `PENDING`, `EXPECTED`, or `""`); `mergeable` is `MERGEABLE`, `CONFLICTING` or `UNKNOWN`; `conflicts` is true only for an `OPEN` PR that is `CONFLICTING`; `ready` is true when the PR is `OPEN`, `APPROVED`, its checks are `SUCCESS`, it is `MERGEABLE` and its merge state is not `BLOCKED`, `BEHIND`, `DIRTY` or `DRAFT`, so a repository with no required review or no checks never reads ready. Every key is always served: before the first poll answers a PR has its `url`, `state` `OPEN`, empty chips and `conflicts` false. `polled_at` (RFC 3339) is the last poll that answered, absent before one has. `stale` is omitted while false and true when the latest poll failed to reach GitHub: the chips are the last known ones, and a failed poll never ends mooring. `no_resume` is omitted while false and true when the record has no valid resume text (`moor.ValidResume`), so the ship cannot be woken and a wake answers 409. A moored ship also carries `stack_ready` (every PR is `ready`; omitted while false), `ready_up_to` (the URL of the highest layer, in the order of `prs`, up to which every layer is `ready`; omitted when the lowest layer is not) and `merge_method` (the repository's default, `merge`, `squash` or `rebase`, asked of GitHub once per repository and fleet; omitted until a poll has it). Its `session` is its slug, `dir` its checkout, `crew` and `subagents` empty and `launched` the stamp of its last launch or wake (empty for a record written before the stamp was kept).

A running ship (any live ship, not only a woken one) carries the PRs it or its crew boats opened: `prs` (the moored shape above), `polled_at` and `stale` appear once it carries one, and `carries_prs` is true while at least one of them is `OPEN`. The poll that serves moored ships asks GitHub about each PR URL that appears in a tool result of the ship's or its crew boats' transcript, and the ship carries it when the fleet's `gh` user opened it at or after the ship's launch. A candidate counts only on the fleet's own GitHub host and for a repository the fleet routes to, with a number that has no leading zero and fits in 31 bits; at most 20 fresh ones are asked per ship per poll. The fleet's poll is one query for the moored and carried PRs and, when a ship has fresh candidates, a second for those alone, so a candidate GitHub cannot answer never makes a ship or the fleet `stale`. A candidate GitHub does not find, or whose query fails three times, is dropped for that launch. Each PR a ship confirms is also kept on its session, so an Admiral restart keeps it. A PR the ship names in its report is not judged this way. A merged or closed PR stays in `prs` with its state but no longer counts for `carries_prs`. `stale` is set only when a PR the ship already carries goes unanswered. A ship woken from its mooring carries its record's PRs at once, before any poll, and keeps them beside the new ones. When the ship exits cleanly, the PRs it carries that are open are checked against GitHub again, with the report's, and the ones still open on the fleet's host are moored with it; if GitHub cannot be reached, the last poll's open PRs stand.

### `GET /api/ships/<session>/pr?url=<PR URL>`

Reads one PR the ship carries, fresh from GitHub through the ship's fleet, with one query and a 15 second limit. It serves running and moored ships; the PR must be one of the ship's `prs`. Response `200` with `{number, url, title, body, state, draft, head, base, review, checks, mergeable, merge_state, failing_checks: [{name, url}], reviews: [{author, state}] (the review history in order, as GitHub recorded it, not folded to one per author), requested: [{login, bot}], threads: [{path, line, resolved, comments}], comments: [{author, body, url, created_at}], stack}`, where an author is `{login, bot}` and `stack` is `{number, size, base, url, entries: [{position, number, url, state, head}]}` or `null`. `body` and comment bodies come whole; the lists are capped: 20 reviews, threads, comments and requests, 10 comments per thread, and only the first 50 check contexts are read for `failing_checks`, and `stack.entries` holds at most 50. `404`: no such ship, a session that is not a ship, an anchored, sailing-off or exited ship, or a PR the ship does not carry. `502`: the fleet has no GitHub access, GitHub failed or did not answer in time. AdmiralBot may not call it.

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

Launches a ship. Body: `fleet`, `harness`, `skill` (`{"kind": "commodore" | "skill" | "none", "name": ...}`), `text`, `model`, `effort`, `autopilot`, `resources`, `source` and `order_id` (a launch from an order; the server reads the order's text), and either `dir` (a checkout or search root of the fleet) or `clone: true` with no `dir`, and optionally `clone_repo`, the `host/owner/repo` the form showed. `crew` is a list of member names to pin, only on a Commodore launch (see below). A launch with `clone: true` derives the repo like the dirs route does, runs `gh repo clone <host/owner/repo> <temporary folder>` in the fleet's environment, moves the result to `<clone_root>/<repo name>` (lower case) and starts the ship there. The client never sends the target path. Only github.com and the host of a GitHub source the fleet has are cloned. `issues` (a list of GitHub issue URLs) goes only on a launch with no `source` (`400` otherwise; an order has its own issue): each must be an `https` issue URL on github.com or the host of a GitHub source the fleet has, and the ship keeps the normalised form. One issue claims that URL, two or more make a slice, one ship holding every claim with `url` the first. The server sets `url` and `title`, never the client. `chat_message` (a chat message id, from the engineer's CLI or UI only) gives the launch the key `chat:<fleet>:<id>`, so one fleet's ids never meet another's. AdmiralBot never sends one: the server drops any it does send, and gives an actor's freeform launch (no order, no issue) the key `chat:<fleet>:<session>-<stamp>`, where `<session>` is the chat session's nonce and `<stamp>` the time of the chat's latest prompt, which the chat's `UserPromptSubmit` hook records, so a message typed in the terminal counts too. One message is one key; the next message, or a new chat session, is a new key. `anyway: true` gives the launch a fresh key instead, so one message can launch twice. An order's key is `<source>:<order id>` and an issues launch's key is its set of issues (`this issue already has a ship` for any overlap, in any order or spelling). A key is held by any listed ship of any status (a moored one included), a pending launch or a launch in flight, and the ship keeps it across an Admiral restart and mooring; it frees when the ship is dismissed or sails off. A woken ship never collides with its own key. `anyway` skips only the chat key, never an order or an issue another ship holds. A launch with no order is freeform: `text` is its prompt and `skill` may name a skill, which the harness runs with the text as its argument (`/skill text`, `$skill text` on Codex). `model` and `effort` are empty for the harness's own default. Claude's `model` is one of `sonnet`, `opus` and `haiku` and its `effort` one of `low`, `medium`, `high` and `max`; Codex takes any plain model name (letters, digits, `.`, `-`, `_`, `:`, never dash-led) and `low`, `medium` or `high`; Copilot takes a plain model name and no effort, since it picks its own.

Answers `201 {"ship": ...}`, or `{"error": "..."}` with:

- `400`: a `crew` that pins on a launch whose skill is not Commodore, a name that is not a lowercase slug of at most 64 characters or is given twice (checked before any clone), a name no layer resolves, or a resolver error in any file of the layers, even an unpinned one (the message lists them); a `model` or `effort` the harness does not offer (the message names which), `autopilot` on a dock whose harness `admiral guard` does not cover (only Claude today) or whose guard probe has not passed in the fleet (the Inbox gets a notice, and a Check again in Preferences runs the probe), a `resources` name the config does not define, an `issues` entry that is not a GitHub issue URL (the message names it), `dir` and `clone` were both sent ("send dir or clone, not both"), no repo can be derived, or the repo is not one gh can clone (a host that is not GitHub, a name that starts with `-`, a dot name), or `source` is `campaign`, whose launches go through `POST /api/campaigns/<id>/launch`;
- `409`: a resource in `resources` is held by another ship (`resource "db" is ...`), `autopilot` when the underway and needs-you ships are already at `max_ships` (`autopilot is at its cap of N ships`), the repo already has a checkout in the fleet, a search root that is itself a checkout included (the message names it: launch there with `dir`), a folder already exists at the target, which is never touched, or `clone_repo` is set and differs from the repo the server derives ("the order's repo changed to X; reopen the form");
- `404`: the fleet is not found; `400`: the fleet has no such dock or no search roots, `dir` is missing, not a directory or not a checkout or search root of the fleet, a skill kind is unknown or a skill name is empty or holds whitespace, Commodore or the dock's executable is not found, or the command line is too long for tmux; `409`: the order already has a ship (`this order already has a ship (<name>)`; AdmiralBot's request, when the holder is in another fleet, gets `a ship in fleet <fleet>` in place of the name, for the order, the chat message and an issue alike; issue claims are machine-wide), the chat message already has one (`this chat message already has a ship (<name>)`), or its URL or any of its `issues` names a GitHub issue that any ship holds, listed in any status or still launching (`this issue already has a ship (<name>)`; the ship leaves the claim when it is dismissed or sails off); `503`: tmux is not on PATH;
- `502`: gh failed, the message holds its stderr on one line with the credentials of any URL in it replaced by `***`, and nothing is left behind;

### Launching as a crew member on `POST /api/ships`

`member` (a crew member name) launches the ship as that member, and only with `skill` `{"kind": "none"}` (Just launch); with any other skill, or sent next to `crew`, it is a `400`, as is a name that is not a lowercase slug of at most 64 characters (checked before any clone). Admiral resolves it over the same layers as pins plus the fleet's memory layer. The member's `harness` is a lock: a `harness` in the body that differs is a `400`, and the fleet needs a dock for the final harness. Its `model` goes after the dock's `args` and Admiral's own model flags, before any extra args, as `--model <m>` (`-m` on Codex), unless it breaks the model name rule (dropped, and the reason goes to the server log) or the dock's `args` already set a model (`400`); a `model` in the body next to a member's model is a `400`. The prompt is the member's instructions, its notes block (`### Every repo` and `### This repo (<owner/repo>)`, the repo read from the launch directory's `origin`), then `text`, joined by a blank line, empty parts skipped. A fix checker takes the body's harness and model and gets no instructions or notes; a member's `skills` are ignored. Warnings go to the server log, not the answer. A member no layer resolves (an ad hoc member is one: ad hoc is not a layer `member` reads), or a resolver error in any layer file, is a `400`.

### Pinned crew members on `POST /api/ships`

`crew` (a list of member names; empty or absent pins nothing) is the only way a launch pins members: the launch text is never read for them. Admiral resolves the names itself over the installed Commodore's `crew/` directory (beside its `SKILL.md`, empty when it has none), the fleet's crew directory `$ADMIRAL_HOME/fleets/<fleet key>/crew/` and the launch directory's `.commodore/crew/`, a later layer replacing an earlier member of the same name. It then writes a byte-exact copy of each pinned member's file as `<name>.md` into the ship's run layer, `$ADMIRAL_HOME/run-crew/<session>/` (0700), and gives the ship `ADMIRAL_RUN_CREW` with its absolute path. A launch with no pins has no `ADMIRAL_RUN_CREW`, whatever a fleet's `env` says, and a crew boat keeps its ship's value. The run layer is removed with the ship's report and inbox, and cleared when a ship name is reused.

A refusal for pins is a `400` before any session exists. One that comes after a `clone` launch has cloned (a name no layer resolves, a resolver error) leaves the clone in place, like any checkout.

### `GET /api/launch-orders/crew` (alias `GET /api/v1/launch-orders/crew`)

Query: `fleet`, `harness` and optionally `dir` (a checkout or search root of the fleet, checked as `POST /api/ships` checks it; without it the repo layer is left out, as for a launch that has not cloned yet). Answers `{"members": [...], "warnings": [...], "errors": [...]}`: each member is `{"name", "role", "pick-me-when", "layer", "harness"}` (`layer` is `built-in`, `fleet` or `repo`; `harness` is the lock, or `null`), `warnings` and `errors` are the resolver's diagnostics. When `errors` is not empty, `members` is empty and a launch that pins would be refused. A harness with no installed Commodore has an empty built-in layer, not an error. `404`: the fleet is not found; `400`: no such dock or `dir` is not in the fleet; `409`: the fleet has no search roots.

### `POST /api/ships/<session>/next` (alias `POST /api/v1/ships/<session>/next`)

Types the next prompt into a ship whose turn has ended, as AdmiralBot's tool `next_prompt` does. Body: `{"text": "..."}`. Answers `200 {"typed": true}` and records a `resumed` outcome with `"live": true`, and `"via":"admiralbot"` for an actor. Errors: `400` for text with a control character, an empty text, one over 16 KiB, and, for an actor, text whose first non-space character is `/` or `!` (AdmiralBot may give a ship a prompt, never a harness command; the engineer's text is unchanged); `404` for an unknown ship; `409` for a ship that is not underway or needing you, one that is mid-turn, and a moored ship (wake it instead).

## Sending to a ship

`GET /api/version` lists the `ship-input` capability when the server has these routes. They are unversioned in the code; `/api/v1/...` reaches them too.

- `POST /api/ships/<session>/messages` with `{"text": "..."}` pastes the text into the ship's pane as one bracketed paste and presses Enter, then clears needs you as `input-seen` does. The text is cleaned first: CRLF and lone CR become LF, control characters other than LF and tab are removed, and the edges are trimmed. Response `{"sent": true}`.
- `POST /api/ships/<session>/crew/<name>/messages`: the same for a crew boat, which clears the boat's needs you.
- `GET /api/ships/<session>/skills` and `.../crew/<name>/skills` list the skills the message box offers: those the ship's or boat's own harness has installed, read in its own checkout (a worktree too) with its dock's identity variables. Response `{"mark": "/", "skills": [{"name": "tdd", "description": "..."}]}`, sorted by name; `mark` is what the harness types before a skill (`$` for Codex, `/` otherwise) and `description` is `""` when the `SKILL.md` has none. `404`: unknown ship or boat.
- `POST /api/ships/<session>/seen` with `{"stop_since": "..."}` marks the ship's stop that began at that `stop_since` as looked at. The pane still needs you, so its question can be answered and its ended turn resumed, but the ship carries `"seen": true` and the new shell leaves it out of the inbox and the Needs you group until a new stop begins. A body for a stop that is over, or a ship that does not need you, changes nothing. Response `{"ship": {...}}`. `404`: unknown ship.
- `POST /api/ships/<session>/interrupt` and `.../crew/<name>/interrupt` press Escape in the pane. They don't clear needs you. Response `{"interrupted": true}`.
- `POST /api/ships/<session>/stop` ends a ship, live or anchored, and its crew boats with it. A live ship's tmux session is killed and the outcome log gets `ended` (signal `KILL`, no exit code) then `stopped`; an anchored ship gets the `ended` it already has, then `stopped`. The ship leaves the list. Response `{"stopped": "<session>"}`. An optional body `{"moor": true}` moors a live ship instead: Admiral checks GitHub at once for the PRs it has open (its carried PRs and its report's, each once; if GitHub does not answer, the carried PRs the last poll saw open stand), writes the moored record, then ends the session as above (`moored`, then `ended`; no `stopped`, `via` stamped on `moored` for AdmiralBot). Response `{"moored": "<session>"}`. `409`: the ship has no open PR (none carried and open, none listed by the report; GitHub failing past the retry window also gives 409 unless carried PRs stand, because then Moor goes ahead with the ones the last poll saw open); the ship is untouched. `500`: the record cannot be written (the ship stays live), or the session would not end (the record stays, and a retried Moor ends it). `400`: the body is not valid JSON. On an anchored ship the body is ignored.
- `POST /api/ships/<session>/return` starts Return to port (ADR 0036) on a live ship and marks it with the session option `@admiral-returning`: `queued` until its own turn ends (needs you, reason `stop`), when Admiral types the clean-up prompt (`ReturnPrompt` in `internal/ships/return.go`) and sets `sent`; when the turn that prompt started ends the same way, the ship is stopped as by `stop`, with `stopped` carrying `"returned": true`. A turn that stops for anything else waits on the engineer. The outcome log gets `returning` (with `via` for AdmiralBot), the ship carries `"returning": true` in `GET /api/ships`, and a second call answers the same. Response `{"returning": true}`. With the body `{"moor": true}` a live ship with an open PR moors instead, exactly as `stop` does: no clean-up prompt is typed, no `returning` is logged, and the response is `{"moored": "<session>"}`; its `400`, `409` and `500` are `stop`'s, and it works on a woken ship that carries a PR. `404`: unknown ship. `409`: the ship is moored, has exited, or carries a PR (`"carries_prs": true`, a live ship that carries at least one `OPEN` PR). A moored ship whose PRs are all done with at least one merged is woken by the poll with its resume text and the clean-up prompt, already `sent` (`woken` has `"by": "return"`, then `unmoored` and `returning`); a woken ship's round then ends `sailed`.
- `POST /api/ships/<session>/answer` and `.../crew/<name>/answer` answer the permission or question menu the pane shows, by sending the harness's keys (`internal/keymap`: Allow once is `1`, Deny is Esc, a question's option is its digit; Copilot's keys are `1`, Esc, and Down then Enter, since its question form ignores digits). The body is `{"card":"permission","reason":"permission:<Tool>","choice":"allow"|"deny"}` or `{"card":"question","reason":"<the pane's reason>","item":"<question item id>","option":<1-based number>}` for one single-choice question, or `{"card":"question","reason":"<the pane's reason>","item":"<first question item id>","answers":[{"options":[<1-based numbers, ascending>],"other":"<free text>"}]}` for a Claude call, one entry per question in order (a single-choice entry has one option or an `other`, a multi-select one at least one option or an `other`; at most 4 questions). Answers go one at a time per pane, and the gate reads the pane's own `@admiral-state` and `@admiral-reason`, not the poll cache: keys are sent only while the pane needs you and its reason equals the body's `reason`. Claude Code also fires PermissionRequest for AskUserQuestion, so a question's pane reason is `question` or `permission:AskUserQuestion`, and the question card accepts either. The permission card refuses `permission:AskUserQuestion` and `permission:ExitPlanMode`, whose menus are not yes or no, and a Codex tool other than `Bash` and `apply_patch`, whose menus are unrecorded. A question also needs `item` to be the newest pending call's first question, with every question of the call pending. Claude's call of any shape (up to 4 questions, multi-select, an Other answer) is answered in place with `answers`, from keys recorded in `internal/keymap/testdata/claude/`. The server sends them one step at a time, reading the pane (`capture-pane`, at most 3 s per step) before each. Claude draws only the bottom of a menu taller than its pane, so for the answer the server grows a window smaller than 120x60 (a phone's or iPad's terminal leaves it so) and then puts back its size and its own `window-size` option: it refuses with `409` and leaves the pane needing you unless the menu is fresh (first tab, cursor on row 1, nothing toggled), each step lands, and the review screen shows the intended answers before the final `1`; Copilot and Codex are answered only for a single single-choice question with `option`. A question item carries the call's `header` chip when it has one. Allow and a question answer clear needs you as `messages` does; Deny leaves the pane needing you with reason `stop`, because the harness sits at its prompt: no hook says so for Claude or Copilot, and Codex's `Interrupt` hook writes the same `stop` after the route does. A Copilot pane is answered in place too: its permission card takes `notification:permission_prompt` and its question card takes `question` or `notification:elicitation_dialog`, each for a pending single-choice `ask_user` of 1-9 options (a several-field form, and a crew boat before its first `agentStop`, go to the terminal). Response `{"answered": true}`. `400`: invalid JSON, an unknown card or choice, or a missing field, whatever the pane shows. `404`: unknown ship or boat. `409` and no keys sent: the pane is not on that card now, has exited, or runs a harness with no key map, or the question is not answerable as above. `502`: tmux refused the keys.
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
- `items`: at most 200, oldest first, the newest 200 without `before`. An item is the model in `internal/conversation` (`id`, `kind`, `text`, and the tool call or question it holds; a tool call carries `name`, `summary`, `output`, `error`, `done` and `input`, what allowing it approves: a Bash call's full `command`, or the path of a Write, Edit, MultiEdit, NotebookEdit or Read call (on Copilot, the `command` of `bash` or the path of `edit`, `create` or `view`), cut at 4 KiB with a marker and omitted for any other tool, while `summary` stays the one-line label, which may be the model's own description; a question carries `text`, `header` (the call's chip for that question, when it has one), `options`, `answer`, `pending` and `multiSelect`, present and true for a question that takes several answers). Ids contain characters that need URL escaping in `before`.
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

## A fleet's Limits

Each fleet in `GET /api/config` carries `preset` (`"work"` or `"home"`, `"work"` when the file has none), `limits`, `sentences` and `overridden`. `limits` is the full effective set: the preset's values with the fleet's own saved Limits layered over them leaf by leaf. Levels are `"A0"`, `"A1"`, `"A3"`, `"A4"` and `"A5"` (there is no A2). `overridden` lists the Limits the fleet stores itself, as the `field` paths of the sentences (`ceilings.t1`, `rates.concurrent`, `wall_clock_minutes`), so a client can mark them and reset them. `sentences` is one `{field, sentence}` per Limit in catalog order (a skip scoped to a routine reads `<value> (<routine name> only)`, or `<value> (a deleted routine only)` once the routine is gone), then two fixed sentences with an empty `field` (a restricted ship never pushes; its network reaches package registries only).

`PUT /api/fleets/<fleet>` takes `preset` and a `limits` object, each optional. Switching `preset` clears the saved Limits except `quiet_hours`. `ceilings`, `rates`, `spend` and `skip` merge per key, and a ceiling merges per leaf too: `{"ceilings":{"t1":{"level":"A5"}}}` leaves `asks_first` as it was, and a T4 save with only `level` and `restricted` keeps its `threshold`. A level that leaves A3 drops the stored `restricted`; one that enters A3 without it takes the preset's, else `investigate`. A threshold is replaced whole, must not lower its ceiling (a level below it, or `investigate` under a `draft_fix` base), and `"threshold": null` removes it. Every other key, lists included, replaces. A `null` for any other key inside `limits` deletes the stored value, so that Limit reads as the preset's again. A refused value is a `400` whose `error` names `fleets.<fleet>.limits.<path>`, and nothing is written. Rules: T3 is at most A1; an A3 ceiling needs `restricted` (`investigate` or `draft_fix`) and no other level has one; `asks_first` is T1 only and `threshold` T4 only. Entries sent in `repo_allowlist` that the fleet does not already store (compared case-insensitively) must be repos under the fleet's search roots, so a checkout that moved never blocks a save that keeps its entry; an unknown fleet is a `404` before that check. A `write_deny` entry has no leading or trailing whitespace, control characters, `(`, `)` or `,`. Bounds: lists at most 5000 entries, quiet-hour windows at most 100, launches per hour and per sender per day 1 to 1000, concurrent 1 to 100, wall clock 1 to 1440 minutes, quota hold 1 to 100, spend 0 to 10000 dollars in whole cents, threshold events 1 to 100000 and days 1 to 365.

## A fleet's chat

Each fleet whose chat is on (`chat.on` in `GET /api/config`) has its own AdmiralBot: a session of the fleet's chat harness (Claude Code or Codex; Copilot is not launchable yet) named `_chat-<fleet>` that runs in `$ADMIRAL_HOME/chat/<fleet>/`. Every route is also served under `/api/v1/...`. A fleet that doesn't exist is a 404, and one whose chat is off is a 409 whose `error` is the reason (no dock, a key that differs from an earlier fleet's only in case, and so on). The notes and instructions routes below are the exception: they only read and write files, so they stay served while the chat is off, and refuse only the case clash, since both keys would share one directory. The old global `/api/chat/*` routes are gone and answer 404.

In `GET /api/config`, each fleet's `chat` object carries the AdmiralBot settings and what the fleet may choose:

- `harness`, `model`, `effort` and `context_threshold` are the fleet's settings; `on` and `off_reason` say whether the chat runs.
- `harness_options` has one entry for each of `claude`, `codex` and `copilot` the fleet has a dock for, in that order: `harness`, `offered`, and when it isn't offered `reason` (one line naming the harness and the missing capability, with the probe's detail when it failed) and `capability` (`bot.mcp_fence` or `bot.config_home`); both are `""` when offered. `models` and `efforts` are the lists that harness's chat accepts, the default first (`""` is the harness default effort). A harness is offered when its probes say so for this fleet (ADR 0035): Claude unless `bot.mcp_fence` failed, Codex and Copilot only with `bot.mcp_fence` and `bot.config_home` both passed.
- `harnesses` is the offered subset of `harness_options`, by name.
- `models` and `efforts` are those of the fleet's own `harness`. Claude takes `sonnet`, `opus`, `haiku` and efforts `low`, `medium`, `high`; Codex `gpt-6.1-sol` and `gpt-6-luna` with the same efforts; Copilot `gpt-5.6-sol` and `gpt-5.6-luna` with no effort.

`PUT /api/fleets/<fleet>` with a `chat` object checks what it sends. Naming a `harness` that isn't offered is a `400` with its reason, and so is a model or effort outside the harness's lists, checked against the harness the save leaves the fleet on. A save that names no harness is not checked against the offered list, so a fleet whose harness lapsed can still change its threshold. Switching harness checks the model and effort the fleet keeps against the new harness, and refuses the save when either isn't one it takes (Codex `high` to Copilot with no `effort` sent is a `400`).

### `GET /api/chat/<fleet>/session`

```json
{
  "fleet": "harbor",
  "running": true,
  "state": "idle",
  "messages": [
    {"id": "u-1", "kind": "message", "role": "user", "text": "...", "at": "2026-11-11T10:00:00Z"},
    {"id": "q-7c1", "kind": "question", "text": "Which repo?", "at": "...", "card": {}},
    {"id": "reset-1794455969929283000", "kind": "reset", "text": "New chat", "at": "..."}
  ],
  "questions": [],
  "error": "..."
}
```

- `state` is `idle`, `working` or `waiting`, and `""` while the chat isn't running. `error` is present when something needs saying: why the last start was refused, or that the transcript can't be read.
- `messages` is the newest 200 entries of the fleet's history (`HistoryWindow` in `internal/chat`), oldest first, served from Admiral's log whether or not the chat is running. `kind` is always set: `message` (with `role` `user` or `assistant`), `question` (a chat question as a card: `text` is its title and `card` is the question item as asked, without its answer) `reset` (a marker: `text` is `New chat` or `Context refreshed`) or `commitment` (a card the server posts when a commitment settles, with no chat turn: `id` is `commitment-<commitment id>-<outcome>`, `text` is the commitment's text and `card` is `{"commitment", "outcome", "ship", "reason"}`; `outcome` is `launched` (`ship` is the session), `reminded`, `cant_run` (`reason` says why) or `dropped` (`reason` says why) or `aged` (a commitment with no trigger turned 14 days old; it stays open and its card comes once)). or `answer` (what the engineer chose on a proposal the runner then removed: `id` is `a-<question id>`, `text` the chosen labels, no `card`; the thread folds it into that question's card). Each `id` appears once.
- `questions` are the fleet's chat questions, each the inbox's question item with `session` `_chat-<fleet>` and `answer`: the engineer's answer or `null`. While one waits it is also an Inbox item (see Inbox below), except a proposal (id `campaign-…`), which only the chat shows.

### The log

Admiral appends the history to `$ADMIRAL_HOME/chat/<fleet>/conversation.jsonl`, one JSON entry a line: `id`, `kind`, `role`, `text`, `at`, `card`, and `logged`, when Admiral wrote it. An entry whose `id` is already in the log is skipped, so reading the transcript again, after a restart too, adds nothing. On the first append of a new month (UTC) the file is renamed `conversation-YYYY-MM.jsonl`; old months are kept. The log is Admiral's: nothing in it is fed back to the model, though the chat can read its own file in its working directory.

### `POST /api/chat/<fleet>/start`

Starts the fleet's chat session or leaves a running one alone, and answers `{"running":true}`. Admiral doesn't start one at server start. `501` naming the harness when the fleet's chat harness is Copilot, which launches no chat yet.

### `POST /api/chat/<fleet>/messages`

`{"text"}` pastes the text into the chat and presses Enter. 400 for empty text or over 16 KiB. 409 when the chat isn't running, no transcript has been recorded yet, or Admiral is refreshing the context (`Refreshing`).

### `POST /api/chat/<fleet>/new`

Clears the model's context and appends a `reset` entry to the log; the earlier history is still served. An idle chat first gets the hidden flush turn (the held answer takes up to `ADMIRAL_CHAT_FLUSH_WAIT`, 3 minutes); the flush and its reply are not served or logged. It sends `/clear` when the running session's model, effort and fleet match the fleet's chat settings, else it ends the session and starts a new one. It withdraws the fleet's chat questions. 409 when the chat isn't running.

### `POST /api/chat/<fleet>/questions/<id>/answer`

Takes the inbox's question answer body and writes the answer file. 404 for an unknown id, 409 when it is already answered, 400 for an invalid answer. A question in one fleet's chat can't be answered through another fleet's route.

### WebSocket

`{"type":"chat","fleet":"<fleet>", ...}` carries the fields of the session beside `type`. It is sent on connect, one per fleet whose chat is on, and again within about a second of a change. Messages are coalesced per fleet, so one fleet's update never replaces another's.

### `POST /api/chat/questions` and `GET /api/chat/questions/<id>/answer` (AdmiralBot only)

These two serve AdmiralBot's `ask_question` tool and need an actor token: any other caller is `403`, and nothing is written. The fleet is the token's. `POST` takes `{"title", "questions"}`, checked as the inbox checks a question item, writes the question file and answers `201 {"id"}`; `400` for a bad question. `GET` holds the request for up to `wait` seconds (a non-negative integer, at most 110) and answers `200` with `{"state": "answered", "answer": {...}}`, `{"state": "withdrawn"}` once the question is gone (a New chat), or `{"state": "waiting"}` at the deadline. A bad id or `wait` is `400`. A question of another fleet is never answered.

### `POST /api/chat/notes` (AdmiralBot only)

Serves the `save_note` tool and needs an actor token: any other caller is `403`, and nothing is written. The fleet is the token's. The body is `{"text"}`; the text is trimmed, and must hold 1 to 500 characters. `201` answers the note `{"id", "text", "ts", "via": "admiralbot"}`. A refused note is `400` for a body that isn't that shape and `422` with an `{"error"}` AdmiralBot can act on for an empty or over-long text, a text holding a character that shows nothing (a control other than newline and tab, a zero-width, bidi-override or tag character, a private-use or unassigned code point), or a full ledger (30 open notes; the message says the engineer can remove notes on Preferences).

### `GET /api/chat/<fleet>/notes`

The fleet's open notes, oldest first: `{"notes": [{"id", "kind", "text", "ts", "via"}]}`, empty for a fleet with no ledger. `kind` is `note` (commitments are listed apart, below). `ts` is RFC 3339 to the second. `via` is absent on a note the engineer wrote. Served while the fleet's chat is off; `404` for a fleet that isn't configured, `409` for a key that clashes in case with an earlier fleet's (`POST /api/chat/notes` too). An actor token can't call it.

### `POST /api/chat/commitments` (AdmiralBot only)

Serves the `make_commitment` tool and needs an actor token: any other caller is `403`, and nothing is written. The fleet is the token's; a `fleet` in the body is ignored. The body is `{"text", "trigger": {"kind", "subject"}, "action", "launch"}`, one JSON value of at most 64 KiB. `text` is trimmed and holds 1 to 500 characters. `trigger.kind` is `ship_sailed` or `ship_anchored` (`subject` is a ship of the fleet), `pr_merged` or `pr_closed` (an https pull request URL on a GitHub host the fleet uses), `issue_closed` (an https issue URL on such a host), `time` (a future RFC 3339 time) or `none` (no subject). `action` is `remind` (no `launch`) or `launch`, whose `launch` is a `POST /api/ships` body: it is stored with the fleet set, `autopilot` true and no `anyway` or chat message, and a launch of its own fleet only, from an existing checkout (`dir`) or a clone (`clone: true`, with no `dir`). It is judged at record time by the rules `POST /api/ships` applies that need no free capacity (`launcher.CheckSpec`: dock and its executable, a codex dock's args, skill, member, pins, model, checkout inside the fleet's roots, Commodore found), for a clone also `launcher.CheckClone` (the fleet can launch and has a place to clone into, the order's repo or the first repository URL in the text names a host the fleet uses, `clone_repo` matches it, and no checkout of it exists yet; nothing is cloned and the checkout's own checks wait for the launch), plus: the source belongs to the fleet and lists the order, no issues beside a source, issue URLs on the fleet's hosts, known resources, no control characters in the prompt, a spec of at most 16 KiB, and autopilot proven for the harness with its advisor (without the Inbox notice a live launch posts). The stored trigger is canonical: a PR or issue subject is the URL rebuilt from its parsed parts (host lowercased) and a time is UTC; a ship trigger also carries `launched`, the matched ship's launch stamp, so a later ship that reuses the session name is not taken for it; a ship with no stamp is refused, since a trigger on it could never fire. A refusal is `4xx` with an `{"error"}` and stores nothing; `422` when the ledger is full (30 open or waiting). `201` answers `{"id", "text", "trigger", "action", "launch", "ts", "via"}`. The same text and trigger may be recorded twice.

### `POST /api/chat/campaign-commitments` (AdmiralBot only)

Serves the `work_on_campaign` tool and needs an actor token: any other caller is `403`. The fleet is the token's. The body is `{"reference", "harness"}`: the campaign's parent issue as a URL or `owner/repo#n` (a bare `#n` is refused), and the harness its children run on (`claude` when absent). The route follows the campaign as `POST /api/campaigns` does, so its `400` and `422` pass through; a campaign that belongs to another fleet is `403`, and `422` when autopilot is not proven for the harness with its advisor (answered in the chat, with no Inbox notice). It records one commitment: trigger `campaign` (the subject is the campaign's id), action `work`, `harness`. `201` for a new one, `200` with the open one when the campaign already has it. Admiral never writes to the issue tree. The generic `POST /api/chat/commitments` refuses trigger `campaign`, and a launch from source `campaign` is refused there too.

A campaign commitment stays open across its children and closes only when the campaign ends (every issue closed, or unfollowed: `ended` card) or the engineer cancels. On each tick it launches the campaign's ready Without-you children in autopilot with `Decide`'s rules (autopilot mode: ADR 0034), one launch key `commitment:<id>:<child url>` each, and records a child event per child: `child_launched`, `child_waiting`, `child_declined` or `child_failed`, with `url`, plus `ship` on `child_launched` and `reason` on `child_failed` (the commitment's `harness` is on its first event). A commitment lists as `{..., "harness", "children": [{"url","state","ship","reason"}], "launched", "waiting_children"}`, one entry per child URL with its last state. At most 200 child events are kept for a commitment; a launch can record two, so it launches no more once fewer than two remain, and posts one `too_large` card. A campaign missing from the follow store ends the commitment ("stopped following").

A campaign commitment also proposes the ready With-you children, one at a time per fleet, when the fleet has chat on (with chat off it proposes nothing and holds the fleet). A proposal is a chat question with id `campaign-<commitment id>-<4 hex of the child URL hash>-<n>`, title `Start <child> with you? (<campaign>)`, and one single-choice question `start` with options `Yes, start it with you` and `No, skip it`; its card reads "Admiral asks". It is not an Inbox item and never counts as a waiting question, so a reset (new chat) keeps an open one and the brief lists it apart as "Admiral is asking", naming the child as `owner/repo#n` (the card keeps the title). A Yes launches the child as a With-you ship (no autopilot, no cap or gate; a claim, resource or launcher error refuses it); No, or an answer of text alone, declines it. Once the runner has acted on an answer (launched, refused or declined) it removes the question and its answer file; the thread's card keeps what the engineer answered (a log entry `a-<question id>`, kind `answer`) and reads "You answered: …", not Withdrawn. A New chat or reset also clears an answered proposal that was left over, and `questions` lists at most 20 proposals apart from at most 20 of AdmiralBot's own questions. The runner withdraws a proposal when the child is no longer ready, when the campaign ends, and when the commitment is cancelled. A fleet is held, and proposes nothing more, while a proposal is open or a With-you child the commitment launched has a ship that is underway or needs you; a ship started by hand or a Without-you child never holds. More child events: `child_proposed` (`url`, `question`) and `child_withdrawn` (`url`, `reason`: `not ready`, `ended`, `cancelled` or `question removed`); `child_launched` carries `mode` (`with_you` or `without_you`). A commitment lists `proposal: {"url","question"}` while one is open, `children[].state` also reads `proposed` or `withdrawn`, and each proposal and its answer takes two of the 200 child events.

Cards: a launched or can't-run card for a child carries `child` (the child's URL) and has the id `commitment-<id>-<outcome>-<4 hex of sha256 of the URL>`; `ended` and `too_large` cards are `commitment-<id>-<outcome>`.

### `GET /api/chat/<fleet>/commitments`

The fleet's open and waiting commitments, oldest first: `{"commitments": [{"id", "text", "trigger", "action", "launch", "ts", "via", "waiting", "wait_reason", "shown"}]}`. `shown` is true once the engineer was asked "still want this?" (the mark stays on an open commitment). `waiting` is true once a launch commitment fired and is held for cap room, a resource or quota, and `wait_reason` (left out when empty) is the refusal's wait text; closed ones (done, dropped, cancelled) are not listed. Served while the chat is off. An actor token can't call it.

### `DELETE /api/chat/<fleet>/commitments/<id>`

Cancels the commitment as the engineer and answers `204`. `404` naming a commitment for an id that is unknown, closed, another fleet's, or a note's. An actor token can't call it.

### `GET` and `PUT /api/chat/<fleet>/instructions`

The fleet's instructions, which only the engineer writes: `{"text", "max": 2000}`, `text` empty when there are none. `PUT` takes `{"text"}`, stores it trimmed (whitespace and a byte order mark) and answers the same shape; empty text clears them. The cap is 2,000 Unicode code points after trimming: more is `422` with an `{"error"}` saying by how many characters. `400` for a body that isn't `{"text": string}`. They live in `chat/<fleet>/instructions.md`. Same fleet rules as the notes route; an actor token can't call either method.

### `DELETE /api/chat/<fleet>/notes/<id>`

Cancels the note as the engineer (the event has no `via`) and answers `204`. `404` for an id that is unknown or already cancelled. An actor token can't call it.

### `POST /api/chat/<fleet>/notes/<id>/promote`

Moves the note into the instructions: its text is appended after the existing instructions and a blank line (the note alone when there are none), and the note is cancelled, under one lock. Answers `200` with the new `{"text", "max"}`. `404` as for delete; `409` with an `{"error"}` when the note holds a character that shows nothing (as `POST /api/chat/notes` refuses; a note saved before that check can't be promoted), or saying by how many characters the result would pass the 2,000 cap (the blank line counts), with the instructions and the note unchanged. An actor token can't call it.

The ledger lives in `$ADMIRAL_HOME/chat/<fleet>/ledger.jsonl`, an append-only log with one event a line: `{"v": 1, "ts", "event", "id", "kind", "text", "via"}`. `event` is `added` or `cancelled` for a note; `kind` is `note` or `commitment`, `text` comes on `added`, and `via` is `admiralbot` when AdmiralBot wrote the event and absent for the engineer. A commitment's `added` also carries `trigger` (`{"kind", "subject"}`), `action` and, for a launch, `launch`; its later events are `fired` (a launch commitment now waiting, with the wait text as its `reason`), `shown` (the "still want this?" mark, set when its `aged` card is posted; it stays open), `done` and `dropped` (each with a `reason`) and `cancelled`. A closed commitment never changes again. The caps count per kind: 30 open notes and 30 open or waiting commitments. Reading folds the events; a line that doesn't parse is skipped.

## Inbox

The items ships leave for you: a question, a review, findings, and Admiral's own notices, wake proposals and crew items. Every route is also served under `/api/v1/`.

### `GET /api/inbox` (alias `GET /api/v1/inbox`)

```json
{"items": [{"version": 1, "id": "q-1", "kind": "question", "title": "Which port?", "created": "2026-10-22T10:00:00Z",
  "session": "s1", "name": "Dory", "harness": "claude", "fleet": "work", "fleet_name": "Work", "fleet_color": "#38bdf8",
  "question": {"questions": []}}]}
```

Oldest `created` first. An item is its file's fields plus `session`, and `name`, `harness`, `fleet`, `fleet_name` and `fleet_color` when the ship is known, live or from the record Admiral kept after it sailed off. Admiral sets those last, so a file cannot name another ship or fleet; they are omitted for an unknown ship and on a notice (session `_admiral`, name `Admiral`). A fleet's waiting chat question is listed too, with session `_chat-<fleet>`, `name` `AdmiralBot` and the fleet from its directory (never from the file); it is not a ship, so Ships never counts it. It leaves the list when answered or withdrawn. The two crew kinds are the exception: session `_admiral`, name `Admiral`, and `fleet`, `fleet_name` and `fleet_color` from the object's `fleet` while that fleet is configured (left off otherwise). `kind` is `question`, `review`, `findings`, `notice`, `wake_proposal`, `crew_notes` or `crew_member`, and the item holds only that kind's own object (`question`, `review`, `findings`, `notice`, `wake_proposal`, `crew_notes` or `crew_member`). The WebSocket `inbox` message, `{"type": "inbox", "items": [...]}`, carries the same list and is sent to each new connection and on every change.

A `crew_notes` item holds `crew_notes: {"fleet", "ship", "run_date", "notes": [{"id", "member", "role", "scope", "repo", "note", "replaces"}]}`. A `crew_member` item holds `crew_member: {"fleet", "ship", "run_date", "member", "role", "ad_hoc_run", "definition", "notes": [...]}`, where `definition` is the ad hoc file's text and the notes are that member's. `fleet` is the fleet key the ship was launched in. The answer files are `{"version": 1, "id", "kind", "answered", "notes": {"<note id>": {"decision", "note", "scope", "repo"}}}` for `crew_notes` and `{..., "decision", "file_removed"}` for `crew_member`.

A migration notice also carries `notice.entries`: `[{"key", "name", "panel", "fleet"}]`, one for each order source panel the old config held (`key` is the source key, `panel` its view id, `fleet` the fleet the source fed: its own, else the first fleet). The shell shows an Automate… link for each; a note already on disk has no `entries` and stays plain text.

### Acting on an item

Each route is `POST /api/v1/inbox/<session>/<id>/<action>` and answers `404` for an unknown item. An item already answered is `409` and final. A `409` is also returned while another action on the item is running (`another action on this item is still running`); that one can be retried.

- `answer`: a question's answers, `{"answers": {"<question id>": {"choice", "other"}}, "note"}`; `400` for an invalid answer. For a chat question (`<session>` is `_chat-<fleet>`) it writes the answer file in that fleet's `chat/<fleet>/questions/`, the same as the chat's own answer route; `404` once the question is withdrawn or the fleet is unknown.
- `submit`: a review, `{"event": "COMMENT"|"APPROVE"|"REQUEST_CHANGES", "edited": bool}`, posted to GitHub through `gh`.
- `discard`: a review, `{}`; deletes the pending review on GitHub. `{"forget": true}` runs no `gh`.
- `findings`: `{"cards": [{"id", "decision": "approved"|"edited"|"rejected", "proposal": {"kind": "diff"|"reply", "content"}}]}`; every card needs one decision and only an edited card takes a proposal. Nothing is posted to GitHub.
- `crew-notes`: a `crew_notes` item (`POST /api/v1/inbox/_admiral/<id>/crew-notes`), `{"notes": {"<note id>": {"decision": "keep"|"edit"|"drop", "note", "scope", "repo", "replaces"}}}`. Every note needs exactly one decision and an unknown note id is `400`. Only an edit takes `note`, `scope` (`fleet`|`repo`), `repo` and `replaces` (a note line without its dash, or `null` for none); a field left out keeps the proposal's value. A kept or edited note is written to the fleet's crew as `- <run_date> (<ship>): <note>`, and a fact the scope already holds is not written twice. A note that doesn't fit (the scope holds 30 and no `replaces`, or `replaces` matches no line) answers `409` `{"error", "note": "<note id>"}` with nothing written; so does a fleet that is no longer configured. A drop writes nothing.
- `crew-member`: a `crew_member` item (`POST /api/v1/inbox/_admiral/<id>/crew-member`), `{"decision": "keep"|"drop"}`, answering `{"answered", "file_removed"}`. Keep writes the ad hoc member into the fleet's crew without its `ad-hoc` lines, then its notes, then removes the ad hoc file; it answers `409` with nothing written when the fleet already has that member, the name is a built-in member of one of the fleet's docks (an ad hoc member never replaces a standing one), the ad hoc file is gone or no longer matches the item's `definition`, a note doesn't fit, or the fleet is no longer configured. Drop removes the ad hoc file only if it still matches `definition`.
- `remove`: Remove from inbox, any item; changes nothing on GitHub and never resumes the ship. Answers `200` with `"forgotten": true`. A notice's file is deleted. On a crew item it counts as Drop: nothing is written to the crew. On a `crew_member` item it also answers `"file_removed"`, whether the ad hoc file went (`false` when the fleet is no longer configured or the file changed or was gone), so a client can tell.

## Crew

A fleet's Crew page: its built-in and fleet members, their notes, and the edits the page makes. Every route is also served under `/api/v1/`, and `{key}` is a fleet key (an unknown fleet is 404). Only the **built-in** and **fleet** layers are listed; repo-layer and ad hoc members are not. Error bodies are `{"error": "<message>"}`. A 400 that names a member field also carries `"field": "<key>"` (`name`, `role`, `based-on`, `skills`, `model`, `harness`, `pick-me-when` or `instructions`). A refusal changes nothing on disk, and no write follows a link: a link anywhere from the crew directory down to the file is refused with 409.

### `GET /api/fleets/{key}/crew[?harness=<dock>]`

`{harness, members, unclaimed, warnings, errors}`. The built-in layer is the Commodore installed for the dock `harness`, which defaults to the fleet's first dock (`""` when it has none, and then the built-in layer is empty); an unknown dock is 400. A missing crew directory reads as empty and a GET never creates it.

A member is `{name, role, based-on, skills, model, harness, pick-me-when, instructions, layer, customized, notes}`. `layer` is `built-in` or `fleet`, the winning layer; `customized` is true for a fleet member with a built-in member of the same name. `notes` is `{fleet: [...], repos: {"<owner>/<repo>": [...]}}`: strings without the leading dash and space, only scopes holding a note, the fix checker's always empty. Only the first 30 notes of a scope show, so lines past 30 in an over-full file can't be reached from the page. A link, a non-directory or an `<owner>` or `<repo>.md` name that is no path segment is skipped with a warning.

`unclaimed` is `[{member, notes}]` sorted by name: every `memory/<name>/` holding a note whose name no built-in or fleet member uses. A repo-layer member's notes show here too, and the set can differ by `?harness=` when the docks' Commodore installs differ. A directory name that is not a member name is skipped with a warning. When the resolver reports `errors`, `members` and `unclaimed` are empty.

### `GET /api/fleets/{key}/crew/skills[?harness=<dock>]`

The skills a member can list, for the Crew page's picker: `{"skills": [{"name", "description", "harnesses", "aliases"}]}`, sorted by `name`, with `harnesses` and `aliases` sorted. `aliases` is every name the skill is installed under on any dock, `name` included, so a member saved with `plugin:name` still matches the entry `name`. With `harness`, only that dock's skills; without it, every dock's. An unknown fleet is 404 and a `harness` the fleet has no dock for is 400; a fleet with no docks answers `{"skills": []}`. It reads each dock's user and plugin skills with the dock's identity variables, never a checkout's skills. One skill is one entry: within a dock a plugin skill `plugin:name` is listed as `name` unless another skill of that dock has the bare name `name`, and then both keep their full names; the same resulting name on several docks is one entry whose `harnesses` lists them and whose `description` is the first non-empty one in dock order (`""` when none has one). A name a saved member would refuse (one that does not start with a letter or digit, or holds a character outside letters, digits and `.` `_` `:` `-`) is left out. The route is not offered to AdmiralBot.

### Fleet members

Edits touch only `$ADMIRAL_HOME/fleets/<key>/crew/`. A member route keys on whether `<name>.md` exists there, so a PUT can repair a broken file. A name with no fleet file but a built-in member is 409 (customize it first); with neither it is 404; a name that is no member name is 400. Each success is 200 `{"name": "<name>"}`. The built-in layer is the `?harness=` dock's, as in GET.

- `PUT /api/fleets/{key}/crew/members/{name}` with `{role, based-on, skills, model, harness, pick-me-when, instructions}` rewrites the file (Go writes the frontmatter and the instructions as the body). It is 400 with `field` when the resolver would refuse the bytes (a parse or field error, a custom role without a valid `based-on`, a built-in role with `based-on`), when `role` or `based-on` differs from the same-named built-in, when a value is not one line, or when a value would read back differently (a skill name that is not valid, the fix checker's skills and instructions). Blank lines around `instructions` are trimmed before the write, as the resolver trims them on read. A file over 65536 bytes is 400 with `field` `instructions`.
- `POST .../members/{name}/customize` copies the built-in member's file into the fleet layer: 404 with no built-in of that name, 409 when the fleet already has it. Its notes stay with the name.
- `POST .../members/{name}/rename` with `{to, carry_notes}` renames a fleet member: `to` is a member name that differs from `name` (400) and names no listed member (409). With `carry_notes: true` its `memory/<name>/` moves to `memory/<to>/`, and the whole rename is 409 if `<to>` already has a note. A `memory/<to>/` that holds no note (what a deleted last note leaves) is replaced; the source having no notes still makes a `<to>` with notes 409. Otherwise the notes stay under the old name, and show in `unclaimed` unless a built-in member of that name claims them.
- `DELETE .../members/{name}` removes the fleet file. Its notes stay; a customized built-in falls back to the built-in member.

### Adding a member

- `POST /api/fleets/{key}/crew/draft` with `{description}` runs one headless judge call (a strong model, 0.30 USD, 2 minutes) with Admiral's bundled crew-writer prompt, and writes nothing, not even the crew directory. The description is the engineer's own text, at most 4000 characters, and goes to the writer as fenced data. It is 400 when empty, blank or longer. It is 409 in two cases, and no call is made in either: `{error, reason}` when no harness is proven to run the judge call, and `{error}` with no `reason` when the fleet's crew path holds a link or a non-directory. A failed call is 502 `{error, kind}` with `kind` `timeout`, `budget spent`, `bad reply` or `error`. A cancelled request cancels the call. A 200 is `{draft, problems}`: `draft` is `{name, role, based-on, skills, model, harness, pick-me-when, instructions}` as the model wrote it (an empty field stays `""`; `harness` is always `""`), and `problems` is `[{field, message}]`, `[]` when the draft would be accepted. A problem's `field` is one of:
  - `name`: not a member name, taken by a fleet member or a built-in of any of the fleet's docks, or `crew/<name>.md` is a link or not a regular file;
  - `role`, `based-on`, `skills`, `model`, `pick-me-when` or `instructions`: the resolver would refuse the file (a custom role with no `based-on`, a skill it would drop, a file over 65536 bytes), a single-line field holds a line break, or the file would be read back differently from what was sent;
  - `skills`: more than 20 skills;
  - any of the above except `name`, for a hidden character; the message names its `U+XXXX`. Hidden means a control, format, private-use or unassigned code point, a line or paragraph separator, a blank filler (U+3164, U+115F, U+1160, U+FFA0, U+2800) or a variation selector, except U+FE0E or U+FE0F directly after an emoji or text-presentation character. `instructions` may hold line breaks and tabs.

  The reply is untrusted model output: show it, and don't act on it.
- `POST /api/fleets/{key}/crew/members` with the PUT body plus `name` creates the fleet file. 201 `{name}`. It is 400 with `field` for a failed check, as the draft's problems; 409 with `field: "name"` when a fleet member or a built-in of any dock has the name, and 409 for a link in the crew directory or at the member file. A create never overwrites.

### `POST /api/fleets/{key}/crew/notes`

`{op, member, scope, repo, note, text, to}`: `op` is `edit`, `move` or `delete`; `scope` is `fleet` or `repo` (with `repo` as `<owner>/<repo>`); `note` is the note exactly as GET returned it. `member` need not resolve, so an unclaimed name works. A `member` or `repo` that is not a valid path part is 400. The note is the first line equal to `- <note>` (a trailing `\r` ignored); none is 409, and the page reloads. Every other line of the file is kept byte for byte.

- `edit` replaces the fact and keeps the date and run. `text` is one line of visible text, at most 500 bytes, and the line must pass the resolver's note check (400).
- `move` takes `to: {scope, repo}`, removes the line and appends it unchanged to the destination, making `memory/<member>/repos/<owner>/` as needed. A destination equal to the source is 400; one that already holds 30 notes is 409.
- `delete` removes the line, and the file when no note is left (also after a `move` out of it). An empty scope is not listed.

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

## Usage

`GET /api/version` lists the `usage` capability when the server has these routes. Both are also served under `/api/v1/`. Figures come from the outcome log's `usage` and `advised` records (ADR 0015), read on each request, plus the live figures of ships still running. Nothing is written. Money is never added across harnesses, and there is no total across harnesses or fleets.

### `GET /api/usage[?fleet=<key>]` (alias `GET /api/v1/usage`)

With `fleet`, one fleet object; `404` `{"error":...}` when the fleet is not found. Without it, `{"fleets":[<fleet object>, ...]}` in the config's order.

```json
{"key":"work","name":"Work",
 "periods":{
  "today":{"from":"2026-11-12T00:00:00-05:00","harnesses":[
   {"harness":"claude",
    "tokens":{"input":1200,"output":300,"reasoning":0,"cache_read":9000,"cache_write":400},
    "money":{"amount":1.32,"basis":"estimate","incomplete":false},
    "spenders":{"ships":{"tokens":{...},"money":{...}},
                "chat":{"tokens":{...},"money":{...}},
                "workers":{"tokens":null,"money":{"amount":0.12,"basis":"estimate","incomplete":false}}}}]},
  "7d":{...},"30d":{...},"month":{...}},
 "quota":[{"harness":"claude","label":"claude","has_data":true,"age_seconds":90,
   "windows":[{"id":"primary","used_percent":40,"window_minutes":300,"resets_at":"2026-11-12T17:00:00Z"},
              {"id":"secondary","used_percent":10,"window_minutes":10080,"resets_at":"2026-11-13T15:00:00Z"},
              {"id":"spend","used_percent":30,"used_usd":6,"limit_usd":20,"period":"monthly","resets_at":"2026-12-01T00:00:00Z"}]},
  {"harness":"codex","label":"codex","has_data":false,"windows":[]}],
 "quota_hold":{"held":true,"harness":"claude","window":"primary","window_minutes":300,"used_percent":93,"line_percent":90,"resets_at":"2026-11-12T17:00:00Z"}}
```

- `periods`: `today` starts at local midnight, `7d` is today and the 6 days before it, `30d` is today and the 29 before it, and `month` starts on the local 1st. `from` is that start in the server's local zone. A record counts in a period when its `ts` is not before `from`.
- `harnesses` is sorted by name, `[]` when the fleet used nothing, and holds a harness only when something was recorded for it. Each entry's `tokens` and `money` are the sum of its `spenders`. A spender is `ships` (ship records, plus the live figures of the fleet's running ships), `chat` (AdmiralBot, ship `_chat`) or `workers` (the `cost` of the fleet's `advised` records, USD `estimate`, no tokens); one with nothing is left out.
- Only a record's top-level `tokens` and `money` are summed: its crew boats are already inside. `tokens` has the five disjoint classes, or is `null` when nothing reported any.
- `money` is `{"amount","basis","incomplete"}` plus `premium_requests` when the harness gave one, or `null`. `basis` is `estimate` (US dollars, Claude) or `credits` (Copilot); Codex has no money. A harness's money is `incomplete` when the harness itself said so, or when some record or live reading it sums had none. `premium_requests` sums the values that are present, and the money is also `incomplete` when some part of the sum had none (and at least one had).
- A running ship's live figures count in every period, whenever it launched; the records of ended ships count in the period of their `ts`. AdmiralBot's live spend is not counted, only its records, so `chat` lags until its next record.
- `advised` records written before they carried a `fleet` belong to no fleet and are left out.
- `quota` has one entry per login of the fleet's docks that has a plan quota (Claude and Codex, in dock order), whether or not a ship is running. `label` is the harness; the login's directory is never sent. `has_data` is `false` after a restart until a figure arrives, and the entry then has no `age_seconds` and no windows. `windows` are `primary` and `secondary` (rate-limit windows, with `window_minutes`) and, on Claude, `spend` (the monthly spend limit). `age_seconds` is how old the last reading is. A window past its `resets_at` reads `used_percent: 0`. Live figures and quota are in memory only.
- `quota_hold` is present only while unattended launches (routine runs, commitments, review-wake rounds) are held for quota: some `primary` or `secondary` window of the first Claude or Codex login that has one is at or above the fleet's `quota_hold_percent` (`line_percent`). `window` says which, `window_minutes` and `resets_at` are as in `quota`, and the hold lifts when the window resets. Spend never counts, and a login with no window figure is never held. Hand launches, AdmiralBot launching on an instruction and accepted proposals are never held. The refusal's `Wait` text, which a commitment keeps as `wait_reason` and a moored ship shows as its waiting text, reads `Held for quota: Claude's 5-hour window is at 93% (line 90%), resets 14:00`.

### `GET /api/ships/<session>/usage` (alias `GET /api/v1/ships/<session>/usage`)

```json
{"session":"saltpaw-ii","harness":"claude","source":"live",
 "tokens":{"input":43,"output":2,"reasoning":0,"cache_read":0,"cache_write":0},
 "money":{"amount":0.3,"basis":"estimate","incomplete":false},
 "age_seconds":7,"at":"2026-11-12T08:00:00Z"}
```

The sum of the ship's `usage` records from its first launch on (one for each round of a moored ship) plus its live figures while it runs. The first launch is the newest fresh `launched` record for the name, so an earlier ship that had the name adds nothing, and month files that end before it are not read; a ship with no `launched` record counts from the start of the log. `source` is `live` when live figures are included (`age_seconds` is theirs), `record` when only records are, and `none` with `tokens` and `money` `null` when there are neither. `at` is the latest record's time, left out when there is none. `404` for a ship not in the ships list.

## Routines and Hold fire

A fleet's routines, their trigger log and Hold fire. A save is checked before it is stored; every route is also served under `/api/v1/`. Every error is `{"error": "…"}`: 404 for an unknown fleet or routine, 400 for a body that isn't a routine or fails a check, 403 for AdmiralBot turning Hold fire off. AdmiralBot's actor may read routines, read Hold fire, turn a routine off and turn Hold fire on (see "AdmiralBot's requests"); every other route here is 403 for it.

A routine:

```json
{"id": "9f2c…", "name": "Nightly sweep", "enabled": true, "when": "30 8 * * 1-5",
 "do": {"launch": {"harness": "claude", "dir": "/path/repo", "text": "sweep the board", "model": "sonnet", "effort": "high", "args": ["--effort", "high"]}},
 "before_acting": "ask_first", "notify": true, "created_at": "2026-11-12T08:00:00Z", "updated_at": "2026-11-12T08:00:00Z"}
```

- `name` is at most 80 characters, with no control characters and no `[` or `]` (400 otherwise, on create and update).
- `off_by` is set by the server when a routine is turned off: `"admiralbot"` when AdmiralBot turned it off. Turning it on clears it, an update keeps it, and a routine the engineer turned off has none.
- `when` is a standard 5-field cron expression (`*`, lists, ranges, steps, month and weekday names) in the server's local time zone, and it must fire at some time in the next five years (`0 0 30 2 *` is refused). A fixed hour runs once on the day clocks go back, not in the repeated hour, and a fixed time the spring-forward gap skips (`30 2 * * *`) runs once at the first instant after the gap, as Vixie cron does; a wildcard hour (`30 * * * *`) runs in every hour that exists.
- `on` replaces `when` for a routine that runs on a source event: `{"source": "<source key>", "kind": "review_requested", "panel": "mine"}`. `panel` is left out unless the source's catalog gives the kind a panel (then it is required). A routine has exactly one of `when` and `on`; an `on` routine is saved with `when` empty, is refused unless the fleet has that source and the source lists the kind, and cannot use a prompt `do`. It never fires on a schedule. An event runs it at Report only or Ask first at most; `act` still reads as a proposal.
- `do` is either `launch` (the launch form's fields, plus `args`, extra harness args the form has no field for) or `prompt` (an AdmiralBot prompt), never both. A launch is refused unless the fleet has the dock, the harness's autopilot guard and the fleet's advisor are proven, and `args` pass the autopilot args allowlist. `resources` names resources the config has (refused otherwise) that the ship holds while it is underway or needs you, and `skill` is the launch form's `{"kind", "name"}` (empty means just launch); both are checked on save and again when the run fires.
- `before_acting` is `report_only`, `ask_first` or `act`. While Hold fire is on, `act` runs as `ask_first`; the stored value doesn't change.

### `GET /api/fleets/<key>/routines`

`{"routines": [<routine>, …]}`, `[]` when there are none. Each routine also carries `last_run: {"at", "outcome"}`, the newest record of its trigger log (see `…/runs`), and none while it has never run. The other routes return the routine without it. The body also carries `spend: {"today_usd", "cap_usd", "reached"}`: the fleet's worker spend today (the sum of every trigger record's `costs` since local midnight, the sum an event is checked against), its `spend.daily_worker_usd` Limit, and whether the first has reached the second. Once `reached` is true, events from this fleet stop at a proposal until the day turns, and the Routines screen says so. When today's trigger log can't be read, `spend` is `{"today_usd": 0, "cap_usd", "reached": true, "unreadable": true}`: events are held at a proposal, as the event path does, and the screen says the spend could not be read. `unreadable` is left out when the read works.

### `GET /api/sources`

Every source, each `{"key", "name", "plugin", "fleet", "icon_url", "capabilities", "has_schema", "state", "read_only", "error", "auth", "warnings", "views"}`. A source that raises events also carries `events: [{"kind", "title", "panel"?}]`, its event catalog: one entry for each kind and panel a routine's `on` may name, with `title` the words to show ("Review requested of you", `New in` and the view's title). `events` is left out until the source has listed them, so a client that needs it waits and asks again.

### `POST /api/fleets/<key>/routines`

Saves the body as a new routine (`id`, `created_at` and `updated_at` are set by the server). `201` with the routine.

### `PUT /api/fleets/<key>/routines/<id>`

Replaces the routine with the body, keeping `id` and `created_at`. `200` with the routine.

### `DELETE /api/fleets/<key>/routines/<id>`

`204`.

### `POST /api/fleets/<key>/routines/<id>/on` and `…/off`

Flips `enabled` and nothing else, and isn't checked again. A turn-off by AdmiralBot sets `off_by` to its `via`; the engineer's leaves it empty. `200` with the routine. Each direction has its own path so the actor guard can allow one.

### `GET /api/fleets/<key>/routines/<id>/runs[?limit=<n>]`

The routine's trigger-log records, newest first (default 20): `{"runs": [{"id", "at", "routine", "outcome", "tier", …}]}`. `outcome` is `launched`, `proposed`, `skipped`, `missed`, `failed`, `reported` or `breaker`. `reported` is a Report only run, whose `action` says what it would have launched; `breaker` is the record written when three launches in a row failed or were undone and `before_acting` was set to `ask_first`. A launch whose ship later ended failed reads `failed` with the reason. A run that waits for a free ship slot, a resource or Admiral's terminal host writes no record until it has launched. An event run's record also carries `event` (the item's URL), `sender` (who did it), `rule` (the routine's id), `reason` (why it was held down or skipped, comma separated), `dedupe_key` (what two events about the same work share) and `costs`, `[{"call", "usd"}]` for the judge calls made for the event; the day's spend sums every record's costs, and the first record an event writes holds them. `tier` is `T1`, `T2` or `T3`. A run still waiting when the next scheduled run comes, when its routine is turned off or deleted, or once it is 12 hours late, writes a `skipped` record saying so. A `before_acting` that is not `report_only`, `ask_first` or `act` (a hand-edited file) is `failed` (`before_acting is not valid`) and launches nothing, Hold fire or not. The start-up catch-up waits for the terminal host to know the ships, and checks the circuit breaker like any run. A prompt run that fails counts toward the breaker like a refused launch. A prompt run is `launched` with no `ship`. A launch that was undone has `undone: true` and `undone_at`. `event`, `sender`, `rule`, `verdict`, `reason`, `action`, `ship` and `via` (`proposal` for a run launched from an Inbox proposal) appear when set. The log is `$ADMIRAL_HOME/routines/<key>/triggers.jsonl`, append-only.

### `GET /api/fleets/<key>/hold-fire`, `POST …/hold-fire/on` and `…/hold-fire/off`

```json
{"on": true, "fleet": {"on": false, "by": "engineer", "at": "2026-11-12T08:00:00Z"}, "global": {"on": true, "by": "engineer", "at": "2026-11-12T08:00:00Z"}}
```

`on` is true when the fleet's switch or the all-fleets one is. `by` is `engineer` or `admiralbot` (a request with AdmiralBot's actor context); AdmiralBot may turn Hold fire on but not off (`403`). Turning a fleet off leaves the all-fleets switch as it was.

### `GET /api/hold-fire`, `POST /api/hold-fire/on` and `…/off`

The all-fleets switch: `{"on": true, "by": "engineer", "at": "…"}`.

### Outside events: `POST /api/fleets/<key>/routines/<id>/mute`, event proposals and event cards

An outside event's text is untrusted and never reaches AdmiralBot. The Lookout turns it into fixed fields; the event ends as an event card in the fleet's chat (Report only) or an event proposal in Admiral's own inbox (Ask first).

- An event proposal is an Inbox item with `kind: "event_proposal"`, `session: "_admiral"`, id `event-<trigger id>` and an `event_proposal` object `{"fleet", "routine", "routine_name", "trigger", "tier", "expires_at"}`. It holds no event text. `launch` and `dismiss` work as for a routine proposal (`POST /api/inbox/_admiral/<id>/launch` answers `201 {"ship": {"session": "<ship>"}}`; `409` when the routine is busy (no room for another ship), is turned off, or the trigger is not an open event proposal for it, for instance one already launched; `410` when the proposal has expired or its trigger or routine is gone; `502` for any other launch failure, with the message; the item stays open after every refusal).
- `GET /api/inbox/_admiral/<id>/event` answers `{"summary", "title", "sender", "link", "dedupe_key", "metadata", "mute_by"}`: the Lookout's summary (or the metadata line when `metadata` is true), shown as untrusted plain text. An actor token can't call it.
- An event card is a chat entry with `kind: "event_card"`, `id` the trigger record's id, `text` the summary and `card` `{"routine", "routine_name", "tier", "sender", "link", "dedupe_key", "metadata", "mute_by"}`. It is never written to the digest.
- `mute_by` lists the Mute rule choices the event allows: `sender` and `dedupe_key`.
- `POST /api/fleets/<key>/routines/<id>/mute` takes `{"by": "sender" | "dedupe_key", "value": "<text>"}` and adds a skip entry scoped to that routine to the fleet's Limits. `204` (also when the entry is already there), `400` for another `by`, an empty or over-long value, a control character or `(unknown)` as a sender, `404` for an unknown fleet or routine. An actor token can't call it.

### Routine proposals and Notify me

An Ask first run (or an `act` run while Hold fire is on) writes an Inbox item in Admiral's own inbox: `kind: "routine_proposal"`, id `routine-<trigger id>` (a fleet key has no length limit, an item id does), `fleet` set so a fleet's list shows it, and a `routine_proposal` object `{"fleet", "routine", "routine_name", "trigger", "action", "reason", "expires_at"}`. `reason` is set when the run was held for quota (`Held for quota: Claude's 5-hour window is at 93% (line 90%), resets 14:00`) and left out otherwise; the run's trigger record carries the same text as its `reason`. `expires_at` is the routine's next run or 24 h, whichever is first; the list drops an expired item. A newer proposal for the same routine closes the older one and writes a `skipped` trigger record (`proposal expired: a newer run replaced it`); an unanswered one writes `proposal expired: nobody answered it before it closed`, and a dismissed one writes `proposal dismissed by the engineer` instead. The open proposals are kept in memory: one still open across a restart gets no expiry record, though the list and Launch still treat it as expired.

`POST /api/inbox/_admiral/<id>/launch` runs the routine once under the same gates and key as a scheduled run (its trigger record has `via: "proposal"`) and answers `201 {"ship": {"session": …}}`; a prompt routine sends its prompt instead. It never waits: with no room or a ship holding the routine's key it answers 409 and leaves the item open. After `expires_at` it answers 410 and runs nothing; a second press answers 409. It also answers 409 when the routine is now switched off (by anyone), runs nothing and leaves the item open until it expires. A launch runs the routine as it is saved now, so an edit since the proposal changes what runs. `POST /api/inbox/_admiral/<id>/dismiss` answers 204.

A routine with Notify me on pushes `launched` (a tap opens the ship), `failed`, `breaker` and `proposed` (a tap opens the proposal's Peek). `skipped`, `missed` and `reported` push nothing. The fleet's Notify switch and the watched-window rule apply as for any push.

## Standing orders

A fleet's Checks (at most 20, each at most 300 characters, none empty or repeated), its Guidance (at most 2000 characters) and its Limits, which a fleet save also edits. The Checks and Guidance live in `$ADMIRAL_HOME/routines/<key>/standing-orders.json`; a worker-launched ship's prompt ends with the Guidance under `## Standing orders`. Every route is also served under `/api/v1/`. Errors are `{"error": "…"}`: 404 for an unknown fleet, 400 for a body that fails a check (the message reads `invalid standing orders: …` and names `checks[i]` or `guidance`), 409 when the config can't be edited. For AdmiralBot's actor only the `GET` is allowed (see "AdmiralBot's requests").

### `GET /api/fleets/<key>/standing-orders`

```json
{"checks": ["Run make check before a PR."], "guidance": "Prefer small PRs.", "preset": "work",
 "limits": {…}, "sentences": [{"field": "wall_clock_minutes", "sentence": "…"}], "overridden": ["wall_clock_minutes"]}
```

`checks` is `[]` and `guidance` `""` when none is filed. `limits` and `sentences` are the fleet's effective Limits, and `overridden` names the Limits the fleet sets over its preset.

### `PUT /api/fleets/<key>/standing-orders`

`{"checks": […], "guidance": "…"}`; a field left out keeps its stored value. Replaces what is stored once the whole result passes the checks. Checks are stored trimmed, so two that differ only by surrounding whitespace count as repeated. `200` with the `GET` shape.

### `POST /api/fleets/<key>/standing-orders/copy`

`{"to": "<fleet key>"}`. Copies this fleet's Checks, Guidance, preset and Limits to the target, replacing its own. `to` must be another fleet (400 otherwise, 404 when unknown); a repo allowlist the target can't satisfy is 400, and nothing changes when the copy is refused. `200` with the target's `GET` shape and `loosens`, the Limits the copy made looser than the target's were.

### `POST /api/fleets/<key>/standing-orders/draft`

`{"prose": "…"}` (1 to 4000 characters). One judge call turns the text into a draft; nothing is saved.

```json
{"draft": {"limits": {"wall_clock_minutes": 30} or null, "checks": ["…"], "guidance": "…" or null},
 "sentences": […], "changes": [{"field": "…", "sentence": "…"}], "loosens": ["wall_clock_minutes"],
 "problems": [{"field": "limits|checks|guidance", "message": "…"}]}
```

A Check or the Guidance that the prose does not hold (compared after whitespace is collapsed, and starting and ending on a word boundary) is dropped and named in `problems`, as is a Limits patch the fleet save would refuse, including a `repo_allowlist` entry outside the fleet's search roots (`draft.limits` is then `null`, as it is when the patch changes nothing the fleet has, so `changes` is empty). The draft counts what the fleet already stores: a Check equal to a stored one is dropped, only as many Checks as fit under 20 beside the stored ones are kept, and Guidance that would pass 2000 characters is dropped when added to the stored Guidance as the stored text, a blank line, then the drafted text. That is how the Guidance is added when the engineer confirms. `changes` are the sentences the patch changes, and `loosens` the Limits it would loosen, which the engineer confirms. `409` with `{"error", "reason"}` when no harness is proven safe for judge calls; `502` with `{"error", "kind"}` when the call fails (`timeout`, `bad reply`, `budget spent` or `error`).

### `POST /api/fleets/<key>/routines/draft`

`{"prose": "…"}`. The same judge call, for a routine: `{"draft": {"name", "enabled", "when", "do": {"prompt": "…"}, "before_acting", "notify"}, "problems": [{"field": "name|when|before_acting|do", "message": "…"}]}`. The draft is a prompt routine and is checked as a save would be; it is not stored. The 400, 409 and 502 answers are the standing-orders draft's.

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

An unpaired device reaches the built shell's files (`/app/…` pages, one-segment `/assets/<name>.<ext>` files and `/sw.js`), the install files, and `GET` or `HEAD /`, which answers 302 to `/app/` with no body, so an installed app can load and ask for a code; none of it holds data. Everything else, the old UI at `/classic/` included, needs a token.

A device is a browser or client that holds its own revocable token. A request from this Mac needs no token. Any other request needs the device's token, as `Authorization: Bearer <token>` or the cookie `admiral_token_<port>`; `?token=` is not accepted. The cookie is HttpOnly and SameSite=Strict, so a request that carries it must also pass the Origin check.

### `POST /api/v1/pairing-codes`

Behind the same auth as the other routes. Answers `{"code":"ABCD-EFGH","url":"http://<host>:<port>/pair?code=ABCD-EFGH","expires":"<RFC 3339>"}`. The code lasts 5 minutes and works once, and a new code replaces the live one.

### `POST /api/v1/pair`

Body `{"code","name"?,"cookie"?}`. A code is case-insensitive, with or without the hyphen. On success it answers `{"token","device":{"id","name"}}` and sets no cookie, so a client sends the token as a Bearer. With `"cookie": true` (the shell's Pair this app screen) it sets the HttpOnly device cookie and leaves `token` out, so the token never reaches a page's script; `name` defaults to a guess from the browser, such as `Safari on Mac`. A wrong, expired or used code is 400; ten failures in 5 minutes are 429 with `Retry-After`; a request from this Mac is 403. `GET /pair?code=<code>` and the form at `GET /pair` (which posts to `POST /pair`) pair a browser instead: they set the device cookie and redirect to `/`.

### `GET /api/v1/devices`

Answers `{"devices":[{"id","name","login"?,"pairedAt","lastSeen","current","notifications","thisMac"?}]}`. `login` is set for a device paired over the tailnet. `current` is true for the device making the request; on the Local route it is true for This Mac. `notifications` is true when the device holds at least one push subscription. `thisMac` is true on the This Mac row only, so a page can tell the Mac from a paired device. The subscription's keys are never listed.

### Web Push: `GET /api/v1/push/key`, `GET`, `PUT` and `DELETE /api/v1/push/subscription`

`GET /api/v1/push/key` answers `{"key":"<base64url>"}`, Admiral's VAPID public key (the `applicationServerKey` for `PushManager.subscribe`), or 503 when the key file could not be read.

`PUT /api/v1/push/subscription` takes the browser's `PushSubscription` JSON (`{"endpoint","keys":{"p256dh","auth"}}`, extra fields ignored) and answers 204. The endpoint must be `https`, `p256dh` a P-256 point and `auth` 16 bytes, both base64url; anything else is 400. It upserts by endpoint for the requesting device, taking the endpoint from another device if that held it. A device may hold several subscriptions, one per browser. `GET /api/v1/push/subscription?endpoint=<url>` answers 204 when the requesting device holds that endpoint and 404 when it does not, including when another device holds it. `DELETE /api/v1/push/subscription` takes `{"endpoint"}`, removes it from the requesting device only and answers 204 even when the device did not hold it.

A paired device subscribes as itself. A request on the Local route (no token) subscribes as the device named `This Mac`, made on the first such request and reused after; it lists and revokes like any device, and revoking it drops its subscriptions. Revoking any device, all devices, or the 90-day expiry drops that device's subscriptions, and so does a 404 or 410 from the push service. Admiral never pushes to a loopback, private, link-local, unspecified or tailnet (100.64.0.0/10) address.

A push is an `aes128gcm` message (RFC 8291) signed with VAPID (RFC 8292), `TTL: 3600`, `Urgency: high`, no `Topic`. Its plaintext is a JSON object: `name` and `line` (always), `tag`, and when set `fleet`, `fleet_name`, `target` (`"peek"` or `"ship"`), `session`, `item`, `crew`, `merge`. Control and bidi characters are stripped from `name`, `line` and `fleet_name`.

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

### The presence message

A window tells the server whether anyone is watching it with `{"type":"presence","visible":true,"idle_ms":1500}` on `/ws/v1`: `visible` is whether the page is visible, `idle_ms` how long ago the person last used it (pointer, key or touch). The shell sends it when the socket opens or reopens, when visibility changes, on the first input after 10 s of none, and every 30 s while visible. The server counts a window as watching while it is visible and `idle_ms` plus the time since the report is under 2 minutes; a window that has not reported, or whose socket closed, does not count. While any window watches, an event that would push is an alert for the watched windows instead. A malformed or negative `idle_ms` is ignored.

### The alert message

The server answers an alert with `{"type":"alert","fleet":"zulu","fleet_name":"Work","name":"Dory","line":"needs your approval","tag":"s-1","target":"peek","session":"s-1"}`, sent only to the windows it counts as watching, never to a hidden, idle or silent one. `name` and `line` are text (the line is the first line, at most 120 characters), `tag` identifies the entry so a repeat replaces the earlier toast, and `target` says where a tap lands: `"peek"` plus one of `item`, `crew` or `merge: true` (or none, for the ship's own stop) for a Peek on `session`, or `"ship"` for the ship's view, which an anchored ship uses because it is never an Inbox entry, or `"routine"` (with `"routine": "<id>"` and no `session`) for a routine run that has no ship: a prompt sent to AdmiralBot, a failed run or a breaker trip. The tap opens `/app/routines/<id>?fleet=<key>`; a run that launched a ship keeps the ship target. The shell chimes and shows a toast for it; a fleet with Notify off sends none.

### The move message

When a config change moves Admiral's listener, a ships-socket client whose path stops working gets `{"type":"move","url":"<address>"}` before it is cut. Mode on: clients that came in over the LAN get the tailnet URL (`GET /api/remote`'s `url`, empty when serve isn't set up). Mode off: clients that came in over the tailnet get the LAN address (`http://<ip>:<port>`). Loopback clients never get it. The server waits up to 2 s for the writes, then cuts the tailnet sockets the change no longer allows, then rebinds; with the mode on, every LAN socket still open is then closed (a client that ignores the message loses its socket). The shell sends the page to `url` when it is an http(s) address other than the page's own; an empty `url` leaves the page where it is. A device that moves to the tailnet pairs again there, since its token and cookie belong to the old origin.

### `DELETE /api/v1/devices/<id>` and `DELETE /api/v1/devices`

Revoke one device or all of them; 204, or 404 for an unknown id. The device's token stops working and its open WebSockets close. A save that fails leaves the device in place and answers 500 `{"error"}`, as it does for a rename.

## AdmiralBot's requests

A fleet's chat calls Admiral with its actor token in the `X-Admiral-Actor` header. The token is `HMAC-SHA256(secret, fleet + "\n" + nonce)`: the secret is a random key in `$ADMIRAL_HOME/actor-secret` (mode 0600) and the nonce is minted per chat session and kept in the tmux session option `@admiral-chat-nonce`, so a token survives an Admiral restart while its chat lives and is new with each chat session. The token is never logged and never put in a command line or a file.

- A request with no header is the engineer's own and is unchanged. A valid token on a loopback request makes it AdmiralBot's for that fleet; an unknown or stale token is 401. A request off loopback is refused: 401 without a device cookie (the auth layer answers first), 403 with one.
- AdmiralBot may call only: `GET /api/ships`, `GET /api/ships/<ship>/conversation`, `GET /api/inbox`, `GET /api/sources`, `GET /api/campaigns`, `GET /api/campaigns/<id>`, `POST /api/ships`, `POST /api/sources/<key>/orders`, `POST /api/inbox/<ship>/<id>/answer`, `POST /api/ships/<ship>/stop`, `POST /api/ships/<ship>/dismiss`, `POST /api/ships/<ship>/next`, `POST /api/ships/<ship>/wake`, `GET /api/outcomes`, `POST /api/chat/questions`, `GET /api/chat/questions/<id>/answer`, `GET /api/fleets/<fleet>/routines`, `GET /api/fleets/<fleet>/standing-orders`, `GET /api/fleets/<fleet>/hold-fire`, `POST /api/fleets/<fleet>/routines/<id>/off`, `POST /api/fleets/<fleet>/hold-fire/on`, `GET /api/hold-fire`, `POST /api/hold-fire/on`, `POST /api/chat/notes`, `POST /api/chat/commitments` and `POST /api/chat/campaign-commitments`, each also under `/api/v1`. A `<fleet>` that is not its own is 403; turning a routine on, turning Hold fire off, and saving, editing or deleting a routine are 403 too, as are `PUT` and `POST …/copy` on `standing-orders` and both drafts (`standing-orders/draft`, `routines/draft`). Every other route is 403, and a path that needs cleaning or decoding to match is refused.
- The list reads (`ships`, `inbox`, `sources`, `campaigns`) hold only the actor's fleet. Touching another fleet's ship, item, source or campaign, or an item or ship of no known fleet, is 403 naming the fleet; a launch must name the actor's fleet and a source of that fleet. A launch body must be one JSON object: data after it is 400 (for the engineer too), and the guard and the handler read the same value. A chat session's own question is the engineer's to answer, so AdmiralBot's answer to an Inbox item of a chat session is 403.
- `GET /api/outcomes[?ship=<ship>][&limit=<n>]` answers `{"outcomes": [<record>, ...]}`, oldest first, the latest `limit` (default 100, at most 1000; `400` for a bad value) of the month files' records. An actor sees only its fleet's: ship names are reused, so a record's fleet is the `fleet` of the latest `launched` record of its ship before it (read in file order); a record with no such record in the files read takes the fleet of the listed or moored ship that holds the name, unless the files hold a later `launched` record of that name, which hides it; a record of no known fleet is hidden. The fence applies before the limit. A `launched` or `woken` record carries `launched`, the ship's launch stamp (RFC 3339; a wake's is the wake time), which a commitment's ship trigger matches on; a record written before that field existed never matches. A `launched` record of a routine's ship has `trigger: {"fleet", "id"}`, pointing at that routine's trigger-log record.
- Writes are stamped `"via":"admiralbot"`: the `launched`, `stopped`, `dismissed`, `answered`, `resumed`, `woken` and `unmoored` outcome records and the launched ship's `via`.
