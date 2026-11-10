# Admiral

Admiral is a visual dashboard that launches coding-agent sessions. It shows the engineer's work queues, lets them hand an order to a harness in one of their fleets by dragging it onto a dock, runs that session in the background, and shows every running session as a ship they can board to read and type into its terminal.

Use these words in UI copy and in code alike.

## Language

**Harness**:
The agent CLI a session runs in: Claude Code, Codex CLI or GitHub Copilot CLI.
_Avoid_: agent, CLI, tool

**Skill**:
What a session starts on, named in the harness's launch prompt with the order's text (`/commodore <text>` in Claude Code).

**Commodore**:
The default skill: it plans the work, assembles a task force of teammates and sees it through to a merged PR. Admiral starts it like any other skill.

**Order**:
One item from an order source, shown as a card in the Orders sidebar, not yet launched: freeform text, a URL or anything else, handed to the chosen skill as it is. Launching an order puts a ship to sea, whether the engineer launches it or autopilot does.
_Avoid_: job, ticket, card (in code)

**Label**:
A tag the order's tracker puts on it, such as `bug` or `P0`, shown on the order's card. Admiral shows labels and never sets them. Labels are not the hot flag: hot is Admiral's own flag, shown as **High**, which a source may derive from labels.
_Avoid_: tag

**Autopilot**:
A panel setting that launches the panel's orders without the engineer, in the order the panel lists them. The panel names the fleet, skill and arguments its ships use. Autopilot launches only while two **caps** have room: the global cap on every ship underway or needing you, hand-launched ones included, and the panel's cap on those launched from that panel. It also launches an order only when every **resource** its panel names has a free unit and passes its check; otherwise the order waits for them. A paused panel launches nothing and leaves its running ships alone.
_Avoid_: auto-launch, bot

**Resource**:
A named thing with a capacity, such as a phone for on-device checks, that a panel's autopilot entry says its orders need. A resource has a number of **units** (its `capacity`, 1 by default), and an optional check that tells whether it is present. A ship that autopilot launched holds one unit of each resource its panel named while it is underway or needing you; a lock taken by hand holds one too. Autopilot launches an order only when every resource it needs has a free unit and passes its check, and the order waits for them until then. Ships launched by hand hold none.
_Avoid_: device, lock (a lock is how a person holds a unit, not the resource), blocked

**Routine**:
The engineer's standing rule for one fleet: when a schedule or a source event fires, do an action, gated by **Before acting**. A scheduled run is the engineer's own act; a source event goes through the Lookout. It is never an order, a commitment or a standing order.
_Avoid_: schedule, automation, cron job

**Before acting**:
A routine's setting for how far it may go on its own: Report only, Ask first or Act. It can only lower what the trigger tier and the fleet allow.

**Trigger tier**:
How far Admiral trusts an outside event, set by who caused it, from T0 (the engineer in Admiral) through T1 (the engineer's own verified chat account), T2 (allowlisted people and trackers, and the engineer's email) and T3 (strangers) to T4 (monitor alerts). An event's text is data, never instructions, unless its tier is T0 or T1.

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
A switch, per fleet or for all of Admiral, that sets every routine to propose only and leaves running ships alone. AdmiralBot or a message from the engineer's own chat account can turn it on; only the engineer in Admiral turns it off.

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
Where a fleet's orders come from. GitHub is built in, one per fleet; other trackers, such as Jira or Linear, are plugins. Every source belongs to exactly one fleet, and there is no freeform list.
_Avoid_: task source, provider, integration, feed

**View**:
A named list of orders from one source, such as GitHub's Assigned to me, Review requested and My PRs, or a custom search.
_Avoid_: panel, column, list

**Campaign**:
A parent GitHub issue Admiral follows as one effort through its tree of children. Admiral reads the tree and launches from it, and never writes to it.

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

**Marina**:
The harbor on the land side of the screen that belongs to one fleet. It holds one dock per harness the fleet allows.
_Avoid_: shipyard, yard (older design names)

**Dock**:
One harness within one fleet (Work Fleet's Claude dock). Dropping an order on a dock opens launch orders for that fleet and harness, as does picking the dock in the phone's dock sheet after tapping an order, and tapping a dock with no order selected opens launch orders without one. A ship waits moored at each dock.
_Avoid_: slot, target

**Launch orders**:
The dialog that opens on a dock, with an order dropped on it or with none. With an order, the engineer chooses what the session starts with: Commodore (the default, a task force), a favorite skill, a custom skill typed in with type-ahead over installed skills, or no skill at all, which sends the order's text to the harness as it is. Without an order there is no skill to choose: the engineer may type the text the harness starts with, or leave it empty to start the harness idle, and the ship has no order.
_Avoid_: launch menu, modal

**Favorite skill**:
A skill the engineer starred, up to five, shown in launch orders after Commodore on every dock where it is installed.
_Avoid_: configured skill, skill list

**AdmiralBot**:
A fleet's coordinator, which the engineer talks to in that fleet's chat from any device. It does no work itself: it reads Admiral and acts by launching ships, adding orders, answering inbox items and keeping its ledger, only on the engineer's instruction or a commitment they agreed to, and everything it writes is marked **via AdmiralBot**. Each fleet has its own, with a disposable context refilled from its brief. It is not a ship and takes no part in caps or resources. When it needs the engineer to choose, it asks a **chat question**, which stays in the chat and also shows in the Inbox while it waits.
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

**Crew boat**:
A smaller boat rafted beside a ship: a teammate the ship's skill started with `admiral crew`, in its own tmux window, which the engineer can board. It may carry a **role** the skill gave it, such as `test-writer`. A crew boat that ended its turn is idle: it waits for its lead, not the engineer.
_Avoid_: child, worker, sub-ship

**Crew member**:
A named, reusable teammate definition in a fleet or repo: its role, skills, instructions, model, optional harness lock, and when to pick it. A crew boat is a crew member at work, and a ship can also be launched as one. The engineer adds a crew member to a ship with **Add crew**, which asks the ship's lead to bring the member on.
_Avoid_: agent, persona, crew type

**Subagent**:
A helper the harness runs inside a session, drawn as a small bot on its ship's deck. It has no terminal of its own, so it cannot be boarded; tapping the deck crew shows each subagent's type.
_Avoid_: crew boat (for a subagent), mark

**Open water**:
The ocean in the middle of the screen where ships sail once launched. Work Fleet ships drift toward the top and Home Fleet ships toward the bottom; the engineer can drag ships anywhere.

**Needs you**:
A ship whose session cannot move until the engineer does: a question, an approval, or a finished turn waiting for the next prompt. It shakes, waves its flag fast and plays a chime.
_Avoid_: alert, blocked, error

**Inbox**:
Everything waiting on the engineer in one queue, oldest first. An **item** is a file a ship wrote in its inbox directory (`$ADMIRAL_INBOX`): a question, a pending review or a set of finding cards. A **needs-you stop** is a ship that needs you right now; it shows in the inbox as a live entry and has no file. **AdmiralBot is waiting** is the other live entry: a fleet's open chat question, answered from Peek or in the chat. The inbox spans every fleet. Answering an item writes an answer file beside it, and the item leaves the inbox. **Remove from inbox** takes an item out without answering it, for one the engineer settled in the ship's terminal. Say "finding card" for a card; items and ships are not cards.
_Avoid_: notifications, tasks, card (for an item)

**Anchored**:
A ship whose harness has exited with an error. It stays anchored, still in open water with a red mark, until the engineer dismisses it. A clean exit with an open PR moors the ship instead, and any other clean exit sails off and disappears.
_Avoid_: docked, finished, done

**Moored**:
A ship carrying an open PR whose harness isn't running, waiting for feedback. It sails off when every PR it carries is merged or closed.
_Avoid_: parked, sleeping, docked, idle

**Feedback round**:
One wake of a moored ship, under the same name, to answer the feedback since the last round.
_Avoid_: review round (Commodore's own review pass)

**Admiral report**:
What a ship's skill leaves for Admiral at the end of every round, its first build and each feedback round: the result, its PRs and the issues each closes, the crew members it used, and the text that resumes it. Never the transcript.

**Bridge**:
The attached-terminal view that opens when the engineer taps a ship: the session's live terminal, which the engineer types into directly, the flotilla, and key buttons on touch devices.
_Avoid_: modal, detail view

**Flotilla**:
The column of the boarded ship and its crew boats beside the terminal, with the ship's subagents listed under them. Picking the ship or a crew boat switches the terminal to that agent; subagents can't be picked.
