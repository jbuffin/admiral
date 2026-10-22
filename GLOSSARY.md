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

**Autopilot**:
A panel setting that launches the panel's orders without the engineer, in the order the panel lists them. The panel names the fleet, skill and arguments its ships use. Autopilot launches only while two **caps** have room: the global cap on every ship underway or needing you, hand-launched ones included, and the panel's cap on those launched from that panel. A paused panel launches nothing and leaves its running ships alone.
_Avoid_: auto-launch, bot

**Order source**:
One configured instance of a plugin that supplies orders: a freeform list the engineer writes first, then GitHub issues and PRs, Jira, Linear and others. The same plugin can be added as more than one source, such as two GitHub accounts. Sources belong to Admiral, not to a fleet. Each source contributes one or more panels.
_Avoid_: task source, provider, integration, feed

**Panel**:
A collapsible group of orders in the sidebar. Each panel belongs to one order source.
_Avoid_: column, list

**Fleet**:
A named setup such as Work Fleet or Home Fleet. It decides which harnesses its docks run and, for each, the executable, configuration directory and environment, so work and personal setups never mix. Fleets come only from Admiral's configuration, which the engineer sets up with Admiral's setup.
_Avoid_: profile, workspace, account, group

**Fleet color**:
The color that identifies a fleet everywhere: its marina sign, the stripe on each of its ships and crew boats, and the launch orders header.

**Marina**:
The harbor on the land side of the screen that belongs to one fleet. It holds one dock per harness the fleet allows.
_Avoid_: shipyard, yard (older design names)

**Dock**:
One harness within one fleet (Work Fleet's Claude dock). Dropping an order on a dock opens launch orders for that fleet and harness, and tapping a dock with no order selected opens launch orders without one. A ship waits moored at each dock.
_Avoid_: slot, target

**Launch orders**:
The dialog that opens on a dock, with an order dropped on it or with none. With an order, the engineer chooses what the session starts with: Commodore (the default, a task force), a favorite skill, a custom skill typed in with type-ahead over installed skills, or no skill at all, which sends the order's text to the harness as it is. Without an order there is no skill to choose: the engineer may type the text the harness starts with, or leave it empty to start the harness idle, and the ship has no order.
_Avoid_: launch menu, modal

**Favorite skill**:
A skill the engineer starred, up to five, shown in launch orders after Commodore on every dock where it is installed.
_Avoid_: configured skill, skill list

**Ship**:
One running harness session, launched from a dock by the engineer or by autopilot, and run in its own tmux session (ADR 0002). Wooden hull, a stripe in its fleet color, the harness logo on its flag, a square bot as captain. Each ship has a generated name, unique among the ships that exist, that it keeps for life; the name carries no meaning.
_Avoid_: boat (for the lead), agent, card

**Crew boat**:
A smaller boat around a ship: a teammate the ship's skill started, which the engineer can board, or a subagent the harness spawned, which is drawn differently and cannot be boarded because it has no terminal of its own.
_Avoid_: child, worker, sub-ship

**Open water**:
The ocean in the middle of the screen where ships sail once launched. Work Fleet ships drift toward the top and Home Fleet ships toward the bottom; the engineer can drag ships anywhere.

**Needs you**:
A ship whose session cannot move until the engineer does: a question, an approval, or a finished turn waiting for the next prompt. It shakes, waves its flag fast and plays a chime.
_Avoid_: alert, blocked, error

**Anchored**:
A ship whose harness has exited. A clean exit sails off and disappears; a ship whose harness exited with an error stays anchored, still in open water with a red mark, until the engineer dismisses it.
_Avoid_: docked, finished, done

**Bridge**:
The attached-terminal view that opens when the engineer taps a ship: the session's live terminal, which the engineer types into directly, the flotilla, and key buttons on touch devices.
_Avoid_: modal, detail view

**Flotilla**:
The column of the boarded ship and its crew boats beside the terminal. Picking one switches the terminal to that agent.
