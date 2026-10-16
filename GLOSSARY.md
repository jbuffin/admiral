# Admiral

Admiral is a visual dashboard on top of Commodore Launcher. It shows the engineer's work queues, lets them hand a task to a harness under a profile by dragging it onto a dock, and shows every running session as a ship they can board to read and type into its terminal.

Admiral reuses Commodore Launcher's vocabulary for everything the launcher already names (see its `GLOSSARY.md`). The naval words below are Admiral's names for those concepts on screen. Use the launcher word in code that talks to the launcher, and the Admiral word in UI copy and UI code.

## Language

**Order**:
One item from a task source, shown as a card in the Orders sidebar, not yet launched. Launching an order turns it into a launcher **Task**.
_Avoid_: job, ticket, card (in code)

**Task source**:
A plugin that supplies orders (GitHub issues and PRs first; Jira, Linear and others later). Each source contributes one or more panels.
_Avoid_: provider, integration, feed

**Panel**:
A collapsible group of orders in the sidebar. A panel shows one task source, or several combined; the engineer can combine and split panels.
_Avoid_: column, list

**Fleet**:
A Commodore Launcher **Profile** as Admiral shows it (Work Fleet, Home Fleet). Fleets come only from the launcher's configuration; Admiral never creates one on its own.
_Avoid_: workspace, account, group

**Fleet color**:
The color that identifies a fleet everywhere: its marina sign, the stripe on each of its ships and crew boats, and the launch orders header.

**Marina**:
The harbor on the land side of the screen that belongs to one fleet. It holds one dock per harness the profile allows.
_Avoid_: shipyard, yard (older design names)

**Dock**:
One harness within one fleet (Work Fleet's Claude dock). Dropping an order on a dock opens launch orders for that profile and harness. A ship waits moored at each dock.
_Avoid_: slot, target

**Launch orders**:
The dialog that opens when an order is dropped on a dock. The engineer chooses what the session starts with: Commodore (the default, a task force), another configured skill, or a custom skill typed in with type-ahead over installed skills.
_Avoid_: launch menu, modal

**Ship**:
One **Session** as Admiral shows it. Wooden hull, a stripe in its fleet color, the harness logo on its flag, a square bot as captain.
_Avoid_: boat (for the lead), agent, card

**Crew boat**:
A smaller boat around a ship: a Commodore teammate, or a subagent the harness spawned.
_Avoid_: child, worker, sub-ship

**Open water**:
The ocean in the middle of the screen where ships sail once launched. Work Fleet ships drift toward the top and Home Fleet ships toward the bottom; the engineer can drag ships anywhere.

**Needs you**:
A ship whose session is waiting on the engineer (a question, an approval). It shakes, waves its flag fast and plays a chime.
_Avoid_: alert, blocked, error

**Bridge**:
The attached-terminal view that opens when the engineer taps a ship: the session's live terminal output, a prompt to type into, key buttons, and the flotilla.
_Avoid_: modal, detail view

**Flotilla**:
The column of the boarded ship and its crew boats beside the terminal. Picking one switches the terminal to that agent.
