# Admiral

Admiral is a visual dashboard that launches coding-agent sessions. It shows the engineer's work queues, lets them hand an order to a harness in one of their fleets by dragging it onto a dock, runs that session in the background, and shows every running session as a ship they can board to read and type into its terminal.

Use these words in UI copy and in code alike.

## Language

**Harness**:
The agent CLI a session runs in: Claude Code, Codex CLI or GitHub Copilot CLI.
_Avoid_: agent, CLI, tool

**Capability**:
Something a harness has been proven to do on this machine, which a feature needs before it turns on. A capability counts only once its probe passes, whatever the harness claims.
_Avoid_: support, feature flag

**Judge call**:
A one-shot, tool-less model call Admiral makes for a decision: the Lookout, the reviewer, the filing drafter, the advisor and the titler.
_Avoid_: worker turn

**Skill**:
What a session starts on, named in the harness's launch prompt with the order's text (`/commodore <text>` in Claude Code).

**Commodore**:
The default skill: it plans the work, assembles a task force of teammates and sees it through to a merged PR. Admiral starts it like any other skill.

**Order**:
One item from an order source, shown as a card in the Orders sidebar, not yet launched: freeform text, a URL or anything else, handed to the chosen skill as it is. Launching an order puts a ship to sea, whether the engineer launches it or a routine or AdmiralBot does.
_Avoid_: job, ticket, card (in code)

**Label**:
A tag the order's tracker puts on it, such as `bug` or `P0`, shown on the order's card. Admiral shows labels and never sets them. Labels are not the hot flag: hot is Admiral's own flag, shown as **High**, which a source may derive from labels.
_Avoid_: tag

**Autopilot**:
The mode a ship is launched in when nobody is watching: low-stakes calls take the recommended default, high-stakes ones become Inbox questions, the guard holds, and it never merges. Every unattended launch uses it; a hand launch uses it only when the engineer ticks it.
_Avoid_: auto-launch, bot

**Resource**:
A named thing with a capacity, such as a phone for on-device checks, that a launch names as needed: a routine's launch, a commitment or the launch form. A resource has a number of **units** (its `capacity`, 1 by default), and an optional check that tells whether it is present. A ship holds one unit of each resource its launch named while it is underway or needing you; a lock taken by hand holds one too. A launch that names a resource is refused unless every resource it names has a free unit and passes its check. A hand launch holds none unless one is named.
_Avoid_: device, lock (a lock is how a person holds a unit, not the resource), blocked

**Routine**:
The engineer's standing rule for one fleet: when a schedule or a source event fires, do an action, gated by **Before acting**. A scheduled run is the engineer's own act; a source event goes through the Lookout. An event routine on the built-in GitHub source fires only for events on a repository among its checkout's remotes. It is never an order, a commitment or a standing order.
_Avoid_: schedule, automation, cron job

**Worker spend**:
The sum of the costs of the judge calls made for outside events, kept on a fleet's trigger log and counted for each calendar day. Once it reaches the fleet's daily cap, events stop at a proposal until the day turns, and the Routines screen says so.
_Avoid_: budget (the per-call caps are separate)

**Mute rule**:
A skip entry the engineer adds from an event card or an event proposal: it skips later events of one routine from one sender, or with one dedupe key. It lands in the fleet's Limits, scoped to that routine, and is removed there.
_Avoid_: block, filter, ignore list

**Allow rule**:
A rule the engineer added through Admiral that lets one harness's ships in one fleet run one narrow kind of call in one repo without asking. A Codex allow rule covers every repo in the fleet. Admiral keeps them; nothing outside Admiral's ships uses them.
_Avoid_: permission, auto-approve, allowlist (that is a Limit's word)

**Permission log**:
A ship's record of every permission prompt it and its crew boats hit, and how each was answered: by a person in the terminal or from a card, by the harness's reviewer, or refused. Allow rule proposals are built from it.

**Allow rule proposal**:
An Inbox item offering an allow rule, built from a prompt a person allowed by hand on a ship. Add keeps the rule; Skip keeps it from being proposed again.

**Never list**:
The calls that can never become an allow rule, however often they were allowed by hand, such as a push, a merge, a delete or a secret read. Admiral fixes it; the engineer can only add to it.

**Before acting**:
A routine's setting for how far it may go on its own: Report only, Ask first or Act. It can only lower what the trigger tier and the fleet allow.

**Trigger tier**:
How far Admiral trusts an outside event, set by who caused it, from T0 (the engineer in Admiral) through T1 (the engineer's own verified chat account or GitHub login), T2 (allowlisted people and trackers, and the engineer's email) and T3 (strangers) to T4 (monitor alerts). An event's text is data, never instructions, unless its tier is T0 or T1.

**Titler**:
The judge call, on the small model, that names a ship launched without an order from its first prompt, and picks the one link the prompt is about when it holds several. Admiral asks it only when the ship's main link is not a GitHub pull request or issue (whose own title wins) and, on Claude Code, when the session's own title has not come.
_Avoid_: summarizer, namer

**Lookout**:
The judge call that reads an outside event. It has no tools and no network, and returns only fixed fields and a short summary marked tainted, from which Admiral's code picks the action.
_Avoid_: worker, worker turn

**Trigger log**:
A fleet's append-only record of every decision Admiral made on a routine or an outside event: tier, sender, rule, reviewer verdict, action and ship. It is not the ledger.

**Restricted ship**:
A ship launched from untrusted text that cannot send anything out: no push, no GitHub token, no web or MCP tools, and network only to package registries. It either investigates read-only or drafts a fix as local commits.

**Promote**:
Starting a new normal ship from a restricted ship's trigger-log record and local work, as the engineer's own act. Pushing and PRs start only here; the restricted session is never continued.

**Hold fire**:
A switch, per fleet or for all of Admiral, that sets every routine to propose only and leaves running ships alone. AdmiralBot can turn it on in its own fleet, and a message from the engineer's own chat account can turn it on; only the engineer in Admiral turns it off, and only the engineer turns it on for every fleet.

**Standing order**:
The engineer's per-fleet rule for work started by outside events, filed as a **Limit**, a **Check** or **Guidance**. It is never an order and never shows in the Orders sidebar.
_Avoid_: rule, policy, permission

**Limit**:
A standing order that Admiral's code enforces: a confirmed setting from a fixed catalog, such as a ceiling, an allowlist, a rate, quiet hours or a skip filter.

**Check**:
A standing order in the engineer's own words that the reviewer judges before a launch.

**Guidance**:
A standing order in the engineer's own words that Admiral adds to a worker-launched ship's prompt as advice.

**Order source**:
Where a fleet's orders come from. GitHub is built in, one per fleet; other trackers, such as Jira or Linear, are plugins. Every source belongs to exactly one fleet, and there is no freeform list. A plugin source can give orders, a page, or both.
_Avoid_: task source, provider, integration, feed

**Plugin**:
A program the engineer installs that Admiral runs for one fleet as a source, outside Admiral's own process. It brings an outside tracker in as orders, a page, or both.
_Avoid_: extension, integration, app

**Module**:
A plugin that has its own page in the fleet's sidebar, for a tracker whose work isn't orders to launch from: tickets the engineer claims, assigns, answers and closes. Admiral draws the page itself from groups of rows and a detail view; a module never supplies its own screen.
_Avoid_: app, extension, page plugin

**Row**:
One thing on a module's page, such as a ticket, listed in a group that says why it is there and opened in the side pane for its detail. It is not an order and never launches a ship itself.
_Avoid_: item (that is the Inbox's word), ticket, card

**Notice**:
A line at the top of a module's page about the module itself rather than one row: an update waiting, a setting still to choose, an upstream read that keeps failing. The module shows it until it stops sending it.
_Avoid_: note (that is a ledger entry), banner, alert

**Module action**:
Something the engineer does to a row, one of its steps or a notice that the module carries out upstream, such as claiming or closing a ticket. It runs on a click, after a yes/no or from a form.
_Avoid_: action (alone; that is the action ladder's word), command

**Built-in action**:
An action a module offers that Admiral carries out itself: opening a link, launching a ship on a GitHub issue the module names, or sending AdmiralBot a message Admiral writes from a row's reference, title and link.
_Avoid_: native action, system action

**Waiting count**:
The number of rows on a module's page that wait on the engineer, shown on its sidebar entry. It is not part of the attention count.
_Avoid_: needs you (that is a ship's state), unread

**View**:
A named list of orders from one source, such as GitHub's Assigned to me, Review requested and My PRs, or a custom search.
_Avoid_: panel, column, list

**Campaign**:
A parent GitHub issue Admiral follows as one effort through its tree of children. Admiral reads the tree and launches from it and never edits it. It may create the parent issue from issues the engineer selects, on their confirm. AdmiralBot never files one itself: on the engineer's instruction it launches a drafting ship or leaves a draft for the engineer to confirm in Admiral.

**Ready**:
A campaign child that can start now: open, with every blocker closed, nobody assigned and no live ship holding Admiral's claim on it.

**With you** / **Without you**:
A campaign child's mode: whether its ship needs the engineer at hand. With-you children start one at a time per fleet; Without-you children can start on their own.
_Avoid_: AFK, HITL, unattended

**Slice**:
Several ready Without-you children of a campaign launched as one Commodore ship, which carries them as one stack of PRs.

**Fleet**:
A named setup such as Work Fleet or Home Fleet. It decides which harnesses its docks run and, for each, the executable, configuration directory and environment, so work and personal setups never mix. Fleets come only from Admiral's configuration, which the engineer sets up with Admiral's setup.
_Avoid_: profile, workspace, account, group

**Project**:
A GitHub repo a fleet owns because it is checked out under the fleet's search roots, so the repo's issues and PRs show in that fleet.

**Owner**:
An org or user account a fleet claims, so its repos route to that fleet when no fleet has them as a project.

**Default fleet**:
The first fleet in the configuration, which takes the GitHub items no project or owner matches.

**Fleet color**:
The color that identifies a fleet everywhere: its marina sign, the stripe on each of its ships and crew boats, and the launch orders header.

**Usage**:
What a harness reports a session used: tokens and plan quota. All three harnesses report it.
_Avoid_: cost, budget

**Spend**:
A money figure that a harness reports itself: Claude's list-price estimate or Copilot's AI credits. Admiral keeps no prices of its own and never adds spend across harnesses.
_Avoid_: cost, budget

**Quota hold**:
A fleet's Limit that holds unattended launches (routine runs, commitments, review-wake rounds) while any plan window of the login is at its line (90% by default) or more, until the window resets. A held routine run becomes a proposal (it asks first); a commitment or wake round waits and launches by itself. Hand launches, AdmiralBot acting on an instruction and accepted proposals are never held.
_Avoid_: cost, budget

**Marina**:
The harbor on the land side of the screen that belongs to one fleet. It holds one dock per harness the fleet allows.
_Avoid_: shipyard, yard (older design names)

**Dock**:
One harness within one fleet (Work Fleet's Claude dock). Dropping an order on a dock opens launch orders for that fleet and harness, as does picking the dock in the phone's dock sheet after tapping an order, and tapping a dock with no order selected opens launch orders without one.
_Avoid_: slot, target

**Launch orders**:
The dialog that opens on a dock, with an order dropped on it or with none. With an order, the engineer chooses what the session starts with: Commodore (the default, a task force), a favorite skill, a custom skill typed in with type-ahead over installed skills, or no skill at all, which sends the order's text to the harness as it is. Without an order there is no skill to choose: the engineer may type the text the harness starts with, or leave it empty to start the harness idle, and the ship has no order.
_Avoid_: launch menu, modal

**Favorite skill**:
A skill the engineer starred, up to five, shown in launch orders after Commodore on every dock where it is installed.
_Avoid_: configured skill, skill list

**Installed skill**:
A skill a harness on this Mac can load for a fleet's dock, from the harness's own skills folder or its plugins. A skill inside a repo counts only for that repo. The same skill can be installed on several harnesses, under its plain name on one and a plugin's name on another, and is still one skill.

**AdmiralBot**:
A fleet's coordinator, which the engineer talks to in that fleet's chat from any device. It does no work itself: it reads Admiral and the fleet's map, and acts by launching ships (a scout, a quick ship or a Commodore ship, its choice), working a campaign, drafting a campaign (a drafting ship, or a draft the engineer confirms in Admiral), adding orders, answering inbox items, giving a ship its next prompt, waking, stopping and dismissing ships, drafting a routine or turning one off, turning Hold fire on in its fleet and keeping its ledger, only on the engineer's instruction or a commitment they agreed to, and everything it writes is marked **via AdmiralBot**. Each fleet has its own, with a disposable context refilled from its brief. It is not a ship and takes no part in caps or resources. When it needs the engineer to choose, it asks a **chat question**, which shows in the Inbox as an "AdmiralBot asked" item while it waits and is answered there or in the chat.
_Avoid_: assistant, chat ship, chat agent, background session, handoff (say "a launch via AdmiralBot")

**Ledger**:
What a fleet's AdmiralBot remembers between resets: its commitments and notes. The engineer can read it and cancel any entry.
_Avoid_: memory, log, history

**Commitment**:
A ledger entry for something AdmiralBot agreed in the chat to do or watch for: a launch or a reminder, run when its trigger fires (a ship sailed off, a PR merged, a time) or held as a plain to-do.
_Avoid_: task, promise, job

**Note**:
A ledger entry for a fact the engineer told AdmiralBot to remember.

**Instructions**:
A fleet's standing guidance for its AdmiralBot, which the engineer writes on Preferences and AdmiralBot can only read. Not a standing order, which is a rule for ships started by outside events.
_Avoid_: system prompt, rules

**Brief**:
The page AdmiralBot starts from after each reset: its instructions, open commitments, what is waiting and running, and the last few messages, drawn from the ledger and live state.
_Avoid_: summary, context

**Ship**:
One harness session and the identity that outlives it, launched by the engineer, a routine or AdmiralBot, and run in its own tmux session (ADR 0002). Wooden hull, a stripe in its fleet color, the harness logo on its flag, a square bot as captain. Each ship has a generated name, unique among the ships that exist, that it keeps for life; the name carries no meaning. A ship that opened a PR stays with it through review: between rounds it is moored, with no session running.
_Avoid_: boat (for the lead), agent, card

**Scout**:
A ship that looks at something (a repo, logs, the web, an issue) and reports back, changing nothing and opening no PR. It runs to the end, leaves its findings and sails off on its own; AdmiralBot sends it and brings its findings to the engineer.
_Avoid_: probe, research ship, investigator

**Quick ship**:
A ship sent for a small change that one session can finish with no design to settle. It runs no skill, in a worktree of its own, and ends with a PR. Anything bigger goes to a Commodore ship.
_Avoid_: fast ship, light ship

**Drafting ship**:
A ship that runs the engineer's chosen skill on their text, with them, to file the parent issue of a new campaign, then names that issue for Admiral to follow. It runs With you, even when AdmiralBot launches it. It is an ordinary ship with a skill, not a scout or a quick ship.
_Avoid_: planning ship, campaign ship

**Drafting skill**:
The skill a fleet remembers for drafting a new campaign: the last one the engineer chose when creating a campaign from text, or none. With one, a new campaign is drafted by a drafting ship running it; with none, the filing drafter drafts it for the engineer to confirm. AdmiralBot follows it unless the engineer names another way, and never changes it.
_Avoid_: default skill, campaign skill

**Launch text**:
What a ship is told to do when it launches. When AdmiralBot launches it, this is AdmiralBot's restatement of the engineer's request (the goal, the repo, what done looks like), with exact strings kept, not the engineer's words as typed.
_Avoid_: brief (that is AdmiralBot's own page), prompt, instructions

**Crew boat**:
A smaller boat rafted beside a ship: a teammate the ship's skill started with `admiral crew`, in its own tmux window, which the engineer can board. It may carry a **role** the skill gave it, such as `test-writer`. A crew boat that ended its turn is idle: it waits for its lead, not the engineer.
_Avoid_: child, worker, sub-ship

**Crew member**:
A named, reusable teammate definition in a fleet or repo: its role, skills, instructions, model, optional harness lock, and when to pick it. A fleet's own crew members live in its crew directory, `$ADMIRAL_HOME/fleets/<fleet key>/crew/`, which every ship of the fleet sees as `ADMIRAL_CREW_DIR`. A crew boat is a crew member at work, and a ship can also be launched as one. The engineer adds a crew member to a ship with **Add crew**, which asks the ship's lead to bring the member on.
_Avoid_: agent, persona, crew type

**Mission**:
A saved Commodore mission file. The fleet's missions directory, `$ADMIRAL_HOME/fleets/<fleet key>/missions/`, holds them and every ship of the fleet sees it as `ADMIRAL_MISSIONS_DIR`. Commodore owns the format of the files; Admiral only keeps the directory. A ship's report can propose a mission; it waits in the Inbox, and Keep writes it there.

**Stop ship**:
The engineer ends a ship that is still running, from its conversation. Admiral kills the ship's session, crew boats included, and records `ended` with a `KILL` signal, then `stopped`. The ship leaves open water as after a dismissal. A ship with an open PR (one it carries, or its report lists) asks first: Moor ends it and keeps the PRs, the other button ends it and forgets them.
_Avoid_: kill, abort

**Return to port**:
A ship ending once it is done, after it cleans up: Admiral tells it to clean up after itself, never how, and stops it like Stop ship when that turn ends. The engineer starts it from the bridge of a live ship once its turn ends; a moored ship starts it by itself once its PRs are merged. A live ship that carries a PR doesn't offer it. A ship with an open PR may moor instead, with no clean-up prompt.
_Avoid_: clean up (for the action), dock, finish

**Hand to Commodore**:
An action on a live Claude ship that carries its settled design to Commodore. The engineer picks a GitHub issue (the default) or Other, free text. Once the ship's turn ends Admiral asks it to file the design as an issue in its repo, or to follow the Other text, and to write the issue URL or a file's path to the **ref file** Admiral names. When the next turn ends Admiral checks the ref, stops the ship and relaunches the same conversation under the same name and checkout as Commodore, with the ref as its order. A bad or missing ref leaves the ship running. The shell shows the action disabled, with the reason, on a Codex or Copilot ship, and doesn't offer it on a Commodore ship; a restricted ship or one an outside event launched refuses it in the sheet. It counts toward the cap like a hand Wake and is never blocked by it.
_Avoid_: promote (the order-to-ship action), transfer, handoff (AdmiralBot's word)

**Subagent**:
A helper the harness runs inside a session, drawn as a small bot on its ship's deck. It has no terminal of its own, so it cannot be boarded; tapping the deck crew shows each subagent's type.
_Avoid_: crew boat (for a subagent), mark

**Open water**:
The ocean in the middle of the screen where ships sail once launched. Work Fleet ships drift toward the top and Home Fleet ships toward the bottom; the engineer can drag ships anywhere.

**Needs you**:
A ship whose session cannot move until the engineer does: a question, an approval, or a finished turn waiting for the next prompt. It shakes, waves its flag fast and plays a chime.
_Avoid_: alert, blocked, error

**Inbox**:
Everything waiting on the engineer in one queue, oldest first. An **item** is a file in an inbox directory: a ship wrote it in its own (`$ADMIRAL_INBOX`), or AdmiralBot wrote a chat question in `chat/<fleet>/questions/`. It is a question, a pending review or a set of finding cards. A **needs-you stop** is a ship that needs you right now; it shows in the inbox as a live entry and has no file. Opening a stop and leaving it unanswered marks it **seen**: it leaves the inbox until the ship's next stop, while the ship still needs you underneath, so its question can be answered later. A chat question shows as an "AdmiralBot asked" item, answered from Peek or in the chat, and has no Remove. The inbox spans every fleet. Answering an item writes an answer file beside it, and the item leaves the inbox. **Remove from inbox** takes an item out without answering it, for one the engineer settled in the ship's terminal. Say "finding card" for a card; items and ships are not cards.
A **routine proposal** is an item for a routine run that waits for a go-ahead (Ask first, or Hold fire); Launch runs it once, Dismiss lets it pass, and it closes at the routine's next run or after 24 hours.
_Avoid_: notifications, tasks, card (for an item)

**Push**:
The system notification Admiral sends to the engineer about something waiting on them, or about AdmiralBot's reply to a report turn, which waits on nothing (ADR 0042).
_Avoid_: notification (for an Inbox item)

**Attention count**:
The number of things waiting on the engineer across every fleet, shown in the tab title, as a favicon dot and as the app icon badge.
_Avoid_: unread

**Anchored**:
A ship whose harness has exited with an error. It stays anchored, still in open water with a red mark, until the engineer dismisses it. A clean exit with an open PR moors the ship instead (see Moored), and any other clean exit sails off and disappears.
_Avoid_: docked, finished, done

**Lost**:
A ship whose session disappeared without ending, through a reboot, a crash or a killed tmux server or session. It keeps its name and waits in open water until the engineer resumes it under the same name or dismisses it.
_Avoid_: crashed, dead, orphaned

**Moored**:
A ship carrying an open PR whose harness isn't running, waiting for feedback. A ship moors when its harness exits cleanly with an open PR, when its report says `done` at the end of its turn with an open PR, or when the engineer picks Moor on Stop ship or Return to port. Once every PR it carries is done and at least one merged, it returns to port; if they were all closed, it sails off.
_Avoid_: parked, sleeping, docked, idle

**Feedback round**:
One wake of a moored ship, under the same name, to answer the feedback since the last round.
_Avoid_: review round (Commodore's own review pass)

**Merge problem**:
Something on an open PR that its own ship can fix and that keeps it from merging: a merge conflict, a branch behind a base that requires up-to-date branches, or a failing CI check of its own.
_Avoid_: not ready, not mergeable, blocked

**Findings**:
What a ship found, written for the engineer and kept with its outcome after it sails off. Every scout leaves findings; any other ship may.
_Avoid_: report (the Admiral report is a different thing), results, summary

**Report turn**:
A turn the server gives AdmiralBot when a ship it launched from the chat finishes, so it can tell the engineer what came back. It is not the engineer's turn: AdmiralBot can read and talk in it, but can't act.
_Avoid_: wake (a moored ship's), notification, callback

**Fleet digest**:
A routine's summary, in one fleet's chat, of what that fleet's ships did since the last one. AdmiralBot writes it from facts Admiral gathers, and can't act while it does.
_Avoid_: brief (AdmiralBot's), morning brief, report, notification

**Admiral report**:
What a ship's skill leaves for Admiral at the end of every round, its first build and each feedback round: the result, its PRs and the issues each closes, the crew members it used, and the text that resumes it. Never the transcript.

**Bridge**:
The view that opens when the engineer boards a ship: its conversation, terminal and PR in tabs, with the flotilla above them.
_Avoid_: modal, detail view

**Conversation**:
What a ship, crew boat or AdmiralBot and the engineer said and did in a harness session, as Admiral reads it from the harness. A moored ship's conversation spans its feedback rounds.
_Avoid_: transcript (the harness's file), log, chat (AdmiralBot's), history

**Flotilla**:
The chips above the bridge's tabs: the boarded ship, then each crew boat with its role. Picking one switches the conversation and terminal to it; subagents get no chip.
