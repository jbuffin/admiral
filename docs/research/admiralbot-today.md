# What AdmiralBot can do today

> Research for [#654](https://github.com/jbuffin/admiral/issues/654), on the map [#653](https://github.com/jbuffin/admiral/issues/653), 2026-11-15. Read against `main` at `17ebe6a1`. The code is the primary source; the ADRs, `GLOSSARY.md` and `docs/agents/codebase.md` are checked against it.

Question: what can AdmiralBot do today? Every capability in one table, in four groups: what it does itself, what it does by launching a ship, what it may do only on the engineer's instruction or an agreed commitment, and what it is guarded from.

## Short answer

AdmiralBot is a chat model with **no built-in tools**. It holds exactly **22 typed tools** from one stdio MCP server (`admiral mcp`). Each tool is a thin HTTP call to the Admiral server with a per-fleet **actor token**. The server allows **26 routes** for that token and fences each one to the bot's own fleet. Everything else returns 403.

- **What it does itself:** reads ships, conversations, outcomes, Inbox, sources, campaigns (list only), routines, standing orders and Hold fire. It asks the engineer chat questions and replies in the chat.
- **What it does through ships:** every piece of real work. That covers `launch_ship` (Commodore runs in autopilot unless "with me"), commitments whose action is a launch, and `work_on_campaign`, after which the server launches a campaign's Without-you children.
- **What needs the engineer's say-so:** every write. The code doesn't enforce "only on instruction". The model is told it in its system prompt and tool descriptions, and the server can't tell an instructed call from an uninstructed one.
- **What is guarded, and how:** by the fence. That means no shell, no files, no other MCP servers, a route allowlist, a per-fleet fence, no harness commands into a ship, and no answering chat questions. The merge/review **guard** (`admiral guard`) does not apply to AdmiralBot. It applies to the autopilot *ships* AdmiralBot launches.

## Where the rules live

| Layer | File | What it enforces |
|---|---|---|
| Harness fence (Claude) | `internal/launcher/bundle.go` `FenceArgs`, `FencePermissions`; `internal/launcher/chat.go` `StartChat` | `--tools ""`, `--strict-mcp-config`, `--permission-mode dontAsk`, `--setting-sources project`, allow `mcp__admiral__*` only, deny Edit/Write/NotebookEdit, `blockReadsOutsideWorkingDirectories`; dock `args` dropped; every `ADMIRAL_*` ship variable unset |
| Harness fence (Codex, Copilot) | `internal/launcher/chat_codex.go`, `chat_copilot.go`; ADR 0023 | per-harness flags; offered only when `bot.mcp_fence` and `bot.config_home` probes pass (ADR 0035) |
| Fence probe | `internal/probes/claude_mcp_fence.go` (and codex/copilot probes) | proves on this machine that the model can't reach a shell, the web or a sub-agent |
| Tool list | `internal/cli/mcp.go` `mcpTools` (lines 209-370) | the 22 tools, their schemas and descriptions |
| System prompt | `internal/launcher/chat.go` `chatPromptTemplate` | "only on what the engineer asks", when to use each tool, Commodore autopilot rule, no invented details |
| Route allowlist and fleet fence | `internal/actor/guard.go` `allowed`, `Guard.fence` | 26 method+path pairs; ship, item, source, campaign and fleet-key checks; launch body must name the bot's fleet; loopback only |
| Per-handler rules | `internal/ships/next.go`, `ships.go`, `claim.go`; `internal/inbox/service.go`; `internal/chat/commitments.go`, `campaigncommit.go`, `notes.go`; `internal/routines/hold.go`, `service.go` | list filtering by fleet, `via admiralbot` stamps, no `/` or `!` prompts, commitment trigger/launch validation, Hold fire one-way |
| Merge/review guard (ships, not the bot) | `internal/cli/guard.go`, `guard_merge.go` | active only under `ADMIRAL_AUTOPILOT=1`: no merge, no APPROVE/REQUEST_CHANGES, no base change, no gate writes, no opaque scripts, no token reads |

## The inventory

"Tool" is the MCP tool in `internal/cli/mcp.go`. "Route" is the server route `internal/actor/guard.go` allows for it.

### 1. Does itself (reads and talk; no instruction needed)

| Capability | Tool → route | Where the rule lives | Notes |
|---|---|---|---|
| List the fleet's ships, status, what each needs | `list_ships` → `GET /api/ships` | mcp.go:209; guard.go `allowed`; `ships.go` `serveList` filters by `actor.FleetOf` | only its fleet |
| Read a ship's conversation (what was said, tool calls, pending questions) | `read_conversation` → `GET /api/ships/{s}/conversation` | mcp.go:210; guard.go kind `ship` | refused for another fleet's ship |
| Read the outcome log (launched, stopped, resumed, dismissed…) | `read_outcomes` → `GET /api/outcomes` | mcp.go:223; `ships/next.go` `serveOutcomes` + `keepFleet` | filtered to its fleet; at most 1000 records |
| List the Inbox | `list_inbox` → `GET /api/inbox` | mcp.go:246; `inbox/service.go` `serveList` | filtered to its fleet |
| List order sources, panels and orders | `list_sources` → `GET /api/sources` | mcp.go:247; `sources/service.go` `serveList` | filtered to its fleet |
| List campaigns (progress and counts, no tree) | `list_campaigns` → `GET /api/campaigns` | mcp.go:248; `campaigns/follow/routes.go` `serveList` | filtered; `Root` is nil'd, so it can't see children |
| List routines | `list_routines` → `GET /api/fleets/{f}/routines` | mcp.go:290; guard.go kind `fleetKey` | fleet comes from env, not the model |
| Read standing orders (Checks, Guidance, Limits) | `get_standing_orders` → `GET /api/fleets/{f}/standing-orders` | mcp.go:291; ADR 0025 | read only |
| Read Hold fire for its fleet | `read_hold_fire` → `GET /api/fleets/{f}/hold-fire` | mcp.go:327 | the global `GET /api/hold-fire` is allowed but no tool calls it |
| Ask the engineer a chat question and wait | `ask_question` → `POST /api/chat/questions`, `GET …/{id}/answer` | mcp.go:328; `chat/ask.go`; ADR 0018 (amended by #272), ADR 0023 | waits up to 100 s, then `wait_id`; also shows in the Inbox |
| Start from a brief and a digest after each reset (Claude only) | SessionStart and UserPromptSubmit hooks, not a tool | ADR 0024; `internal/brief`, `internal/digest`; `bundle.go` `claudeChatSettings` | not on Codex or Copilot ([#493](https://github.com/jbuffin/admiral/issues/493)) |
| Reply in the chat | the harness | `chatPromptTemplate` | "a line or two" |
| Propose a campaign grouping by link | reply text `/app/orders?fleet=…&newCampaign=…` | `chatPromptTemplate` | the engineer files it; no tool |
| Draft a standing order or routine as prose and link to `/app/routines` | reply text | mcp.go:291 description; ADR 0023, ADR 0025 | the engineer files it |

### 2. Does by launching a ship

| Capability | Tool → route | Where the rule lives | Notes |
|---|---|---|---|
| Launch a ship from an order or freeform text, any harness, skill (Commodore, a named skill, none), dir or clone | `launch_ship` → `POST /api/ships` | mcp.go:249, `launchShip` mcp.go:437; guard.go kind `launch`; ADR 0023 "Writes" | body must name its own fleet; source must be in its fleet; stamped `via admiralbot` |
| Commodore launch runs in autopilot unless "with me" | `launch_ship` `with_me` | mcp.go:447; ADR 0023, ADR 0027, ADR 0034 | any other skill runs interactive (see disagreement D1) |
| At the cap, an autopilot launch falls back to interactive | `launch_ship` | mcp.go:456; `ships.go` cap check (only autopilot launches are capped) | see D2 |
| Pin crew members on a Commodore launch | `launch_ship` `crew` | mcp.go schema; ADR 0027 (from skills#195) | |
| Claim GitHub issues (2+ = a slice) on a no-source launch | `launch_ship` `issues` | mcp.go `shipsBody`; ADR 0023 "Duplicates and claims"; `ships/claim.go` | server-side claim only; never assigns on GitHub |
| Second freeform ship for the same message | `launch_ship` `anyway` | prompt; ADR 0023; `ships/claim.go` keys on the chat prompt in force | the server ignores a client-sent message id |
| Chart a campaign: launch a ship on the `wayfinder` skill | `launch_ship` skill `wayfinder` | `chatPromptTemplate`; ADR 0027 | |
| A commitment whose action is a launch (fires later, autopilot, waits for cap and resources) | `make_commitment` action `launch` → `POST /api/chat/commitments` | mcp.go:337, `makeCommitment`; `chat/commitments.go` `checkLaunch`; `internal/commitments/runner.go`; ADR 0024 | needs the harness's autopilot proven; may name `resources` |
| Work on a campaign: the server launches each Without-you child in autopilot until it ends | `work_on_campaign` → `POST /api/chat/campaign-commitments` | mcp.go:357; `chat/campaigncommit.go`; `commitments/campaign.go`; ADR 0027 | With-you children become chat questions asked by the server |

### 3. Only on the engineer's instruction or an agreed commitment

Every write in the table is here. The rule is ADR 0023's "When it may act". It is carried by `chatPromptTemplate` ("only on what the engineer asks in this chat") and by the tool descriptions. **No code checks it** (see D3). A scheduled routine whose Do is "prompt AdmiralBot" also sends a turn marked `[Routine: <name>]` (`routines/scheduler.go:647`, ADR 0025). That turn counts as the engineer's standing instruction.

| Capability | Tool → route | Where the rule lives | Notes |
|---|---|---|---|
| Launch a ship (all of group 2) | as above | ADR 0023 | |
| Add an order to a source's panel | `add_order` → `POST /api/sources/{k}/orders` | mcp.go:264; guard.go kind `source` | only a plugin source that supports adding; freeform orders are gone (ADR 0023) |
| Answer an Inbox **question** with the engineer's choice | `answer_item` → `POST /api/inbox/{s}/{id}/answer` | mcp.go:267; `inbox/service.go` `serveAnswer`; guard.go kind `item` | question kind only; stamped `via`; never a chat question |
| Give a ship whose turn has ended its next prompt | `next_prompt` → `POST /api/ships/{s}/next` | mcp.go:284; `ships/next.go` `serveNext` | refused mid-turn or unless `needs_you` with reason `stop`; no `/` or `!` |
| Wake a moored ship, with optional text | `wake_ship` → `POST /api/ships/{s}/wake` | mcp.go:286; `ships/wake.go` | the text goes after the ship's resume prompt |
| Stop a ship (any ship in its fleet, hand-launched too) | `stop_ship` → `POST /api/ships/{s}/stop` | mcp.go:288; ADR 0023, ADR 0029 | |
| Dismiss an anchored ship | `dismiss_ship` → `POST /api/ships/{s}/dismiss` | mcp.go:289 | |
| Turn one routine off | `turn_routine_off` → `POST /api/fleets/{f}/routines/{id}/off` | mcp.go:293; `routines/service.go`; ADR 0023, ADR 0025 | stamped `by: admiralbot` |
| Turn Hold fire on, for its fleet **or every fleet** | `hold_fire_on` → `POST /api/fleets/{f}/hold-fire/on` or `POST /api/hold-fire/on` | mcp.go:309; guard.go comment "quieter, never louder"; GLOSSARY "Hold fire"; ADR 0025 | the bot's only cross-fleet write (see D5) |
| Save a note | `save_note` → `POST /api/chat/notes` | mcp.go:335; `chat/notes.go`; ADR 0024 | the prompt tells non-Claude chats not to call it, but the route still accepts it |
| Record a commitment (launch or remind; trigger ship sailed/anchored, PR merged/closed, issue closed, time, none) | `make_commitment` → `POST /api/chat/commitments` | mcp.go:337; `chat/commitments.go` `checkTrigger`, `checkLaunch`; ADR 0024 | the server checks the trigger can fire and the launch would be admitted; fires with no model turn |
| Work on a campaign | `work_on_campaign` | mcp.go:357; ADR 0027 | refuses a harness without proven autopilot and advisor |

### 4. Guarded from (never)

| Never | How it is stopped | Where the rule lives |
|---|---|---|
| Run a shell, read or write files, search the web, start sub-agents | no built-in tools; dontAsk; deny Edit/Write; Read outside its dir blocked; proven by `bot.mcp_fence` | `bundle.go` `FenceArgs`/`FencePermissions`; ADR 0023; `probes/claude_mcp_fence.go` |
| Use any other MCP server or the engineer's settings, hooks or plugins | `--strict-mcp-config`, `--setting-sources project`, dock args dropped, stale `CLAUDE.md`/settings removed from the chat dir | `chat.go` `StartChat`, `removeChatStale`; ADR 0020 → 0023 |
| Call any route not in the 26 | 403 "AdmiralBot can't do that" | `actor/guard.go` `match` |
| Touch another fleet's ships, items, sources, campaigns, routines or standing orders | 403 naming the fleet; lists filtered | `actor/guard.go` `fence`; handlers' `actor.FleetOf`; ADR 0023 "Identity and fleet" |
| Use its token from off the Mac or follow a redirect with it | loopback only; no redirects | `actor/guard.go` (`auth.IsLocal`); `mcp.go` `do` |
| Answer harness approvals or questions in a ship | no route (`/api/ships/{s}/answer` not allowed) | `actor/guard.go`; ADR 0023 "Never" |
| Type into a ship mid-turn, or send a slash command or `!` shell line | `serveNext` refuses | `ships/next.go`; ADR 0023 |
| Answer its own chat question | 403 "the engineer's to answer" | `actor/guard.go` kind `item` |
| Submit or discard a review, act on findings, launch or dismiss a proposal | no route; `answer_item` refuses non-question kinds | `actor/guard.go`; `inbox/service.go` |
| Merge a PR, run a hand feedback round, Fix PR, unfollow reviews, close a crew boat | no route | `actor/guard.go`; ADR 0034 "No auto-merge" |
| Create a campaign or edit an issue tree | no route (`POST /api/campaigns/create` refused); no GitHub tool | ADR 0027; `actor/guard.go` |
| Follow or unfollow a campaign by hand, override a child, change its work repo | no route; following only happens through `work_on_campaign` | `actor/guard.go` |
| Save, edit, delete or turn on a routine; file or change a standing order | no route | ADR 0023, ADR 0025; mcp.go:291, :293 |
| Turn Hold fire off | `ErrNotYours` | `routines/hold.go`, `routines.go`; GLOSSARY "Hold fire" |
| Cancel or change a commitment; write instructions; delete a note | no route; the engineer does it on Preferences | ADR 0024; mcp.go:337 description |
| Change config, sources, settings, Preferences, devices | no route | ADR 0023 "Never" |
| Receive a source event's raw text, or be prompted by an event routine | event routines can only launch | `routines/routines.go:693`; ADR 0025 |
| Carry a launch-commitment `anyway` or `with_me` | stripped | `mcp.go` `makeCommitment`; `chat/commitments.go` `checkLaunch` |
| Merge, approve or retarget from an autopilot ship it launched | the ship's PreToolUse guard (`ADMIRAL_AUTOPILOT=1`) | `cli/guard.go`, `guard_merge.go`; ADR 0034; known gaps in `docs/agents/codebase.md` |

What it is **not** guarded from, by design (ADR 0023 "Ships are not fenced"): a ship it launches runs with the fleet's normal setup and the engineer's harness config, MCP tools and GitHub login, like a hand launch. An interactive ship (non-Commodore, or "with me") has no merge guard.

## Where the code and the ADRs disagree

- **D1. Which launches run in autopilot.** ADR 0034 says autopilot mode "applies to plain-skill ships as well as Commodore ships", and that only AdmiralBot launches the engineer marks "with me" stay interactive. ADR 0023, ADR 0027 and the code limit it to Commodore. `autopilot := kind == "commodore" && !a.WithMe` (mcp.go:447). A plain-skill or no-skill ship from AdmiralBot runs interactive with no merge guard.
- **D2. The cap fallback switches mode without asking.** ADR 0023 says an instructed launch "counts toward the global cap but is never blocked by it". The code does that by relaunching an autopilot ship as **interactive** when the server answers "autopilot is at its cap" (mcp.go:456). The cap check only applies to autopilot launches (`ships.go`). No ADR records that the mode changes. The tool returns a `reason` and the prompt tells the bot to report the mode.
- **D3. "Only on the engineer's instruction" isn't enforced in code.** ADR 0023 and ADR 0024 ("Written only on the engineer's instruction") read as rules. They are carried only by the system prompt and the tool descriptions. The actor token can't tell an instructed call from a self-started one, and nothing ties a write to a chat message, except that a freeform launch is keyed on the chat prompt in force (`ships/claim.go`).
- **D4. Resources on an instructed launch.** ADR 0023 says such a launch "holds no resources unless the engineer names one". `launch_ship` has no `resources` field and clears it anyway (`a.Resources = nil`, mcp.go:445), so the engineer can't name one through the bot. Only `make_commitment`'s launch spec takes `resources`.
- **D5. Hold fire on every fleet.** `hold_fire_on` with `all_fleets` (mcp.go:309, `POST /api/hold-fire/on`) is a write that crosses fleets. GLOSSARY, ADR 0025 and the code comment allow it because it only makes Admiral quieter. Map #653 calls fleet segregation a hard rule and puts "anything that crosses fleets" out of scope. Either that rule has this exception or the tool should lose `all_fleets`.
- **D6. Campaigns and autopilot.** ADR 0027 ("Working a campaign") says "Autopilot plays no part". ADR 0034, the tool description and `commitments/campaign.go:216` launch Without-you children in autopilot, and `campaigncommit.go` refuses a harness without proven autopilot. ADR 0027's line is stale. ADR 0034 amends 0023 but not 0027.
- **D7. Reading a campaign.** ADR 0027 amends 0023: "AdmiralBot reads campaigns". The guard allows `GET /api/campaigns/{id}`, but no tool calls it, and `list_campaigns` strips the tree. So the bot can't see a campaign's children, statuses or blockers.
- **D8. "Draft a routine".** ADR 0023 lists "Draft a routine, which the engineer saves on the Routines screen", and ADR 0025 says "AdmiralBot can draft a routine". There is no draft tool or route. The routine drafter (`internal/routines/draft.go`) serves the engineer's "Describe a routine". The bot can only write prose and link to `/app/routines/new` (mcp.go:291).
- **D9. Stale ADR 0018 and GLOSSARY.** ADR 0018's decision text still says "its only write is adding an order". It is marked amended, but a reader of the decision alone gets it wrong. The GLOSSARY entry for **AdmiralBot** lists "launching ships, adding orders, answering inbox items and keeping its ledger". It leaves out next prompt, wake, stop, dismiss, routine off, Hold fire and campaigns.
- **D10. Notes on Codex and Copilot.** `save_note` is offered to every harness. The prompt tells a non-Claude chat not to call it, but the route accepts it, and those chats never read notes back ([#493](https://github.com/jbuffin/admiral/issues/493)).

## Decided but not built, and stale tickets

- **[#493](https://github.com/jbuffin/admiral/issues/493)** (post-redesign): instructions, notes, brief, digest and scheduled resets on Codex and Copilot. Today only a Claude Code chat has memory beyond its context.
- **[#356](https://github.com/jbuffin/admiral/issues/356)** (needs-triage) asks for an `admiral ships stop` CLI command so AdmiralBot can reach Stop through ADR 0020's Bash fence. That fence is superseded (ADR 0023) and `stop_ship` exists over MCP. The ticket is obsolete as written, though a CLI stop for people may still be wanted.
- **D7 and D8 above:** reading a campaign's tree and drafting a routine are decided in ADRs and not built as bot tools.
- **ADR 0025's card actions:** worker launches post a card in the chat with Stop, Undo, Promote and Mute rule. These are engineer actions on cards, not bot tools.

## Gaps that matter for the map

These are not disagreements. They are what the code leaves out, and they matter for "an idea in the chat → work done through ships → reported back":

- **No model or effort choice on a launch.** `launch_ship` takes `harness` but not `model` or `effort`, though `launcher.Request` has `Model`.
- **Results don't come back to the bot as a turn.** Ship events reach it only as the digest prepended to the engineer's *next* message (Claude only), plus cards the engineer sees. Commitments fire "with no chat turn". Nothing wakes AdmiralBot when a ship finishes ([#658](https://github.com/jbuffin/admiral/issues/658)).
- **It can't see a ship's diff, PR or report** except through the conversation. There's no tool for the report file, the Changes tab or a PR's state.
- **It can't read its own ledger** (open commitments, notes) except through the brief, which is Claude only.
- **It can't act on reviews or PRs at all:** no feedback round, no Fix PR, no merge, no review submit. That's by design for merge (ADR 0034), but "come back to a PR waiting for you" ([#659](https://github.com/jbuffin/admiral/issues/659)) stops at the bot's edge.
- **It can't file an issue**, so "filing issues from the chat" (map #653, Not yet specified) needs a ship or a new tool.
