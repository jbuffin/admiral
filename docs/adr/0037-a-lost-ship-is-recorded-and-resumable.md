# 37. A lost ship is recorded and resumable

Status: accepted

## Context

[ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md) let a dead tmux server end every ship with nothing to show for it, and [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md) wrote no `ended` for a session removed without a dead pane. That was cheap while ships were short. A Commodore now runs for hours and a moored ship waits for days, so a reboot is expensive: on 2026-11-13 an unexpected shutdown lost seven ships across both fleets and all three harnesses, and bringing them back took an hour of hand forensics in the outcome log and each harness's session store.

## Decision

**Admiral keeps a launch record for every live ship, and a record with no live session is a lost ship the engineer can resume under its own name.**

- **A launch record** sits on disk for each live ship, as the moored file does for a moored one: name, session, fleet, harness, directory, skill, order fields, crew boats, the last status Admiral saw, and the conversation id once known. The conversation id follows the harness when it changes (a Copilot `/resume`, a Claude `/clear`, a fork). A ship that ends cleanly removes its record.
- **Lost** is found at startup and by the ship poll, so a reboot, a crash, a `kill-server` and an outside `kill-session` are all caught. Admiral writes `ended` with reason `lost` once, and the record keeps the ship's name reserved.
- **The engineer sees it twice:** a `lost` status wherever ships show, with **Resume** and **Dismiss**, and one live Inbox entry for the lost ships with **Resume all**. Dismiss deletes the record and frees the name; it writes no second `ended`.
- **Resume relaunches the ship under the same name** with its harness's resume flag. A ship whose conversation can't be found offers **Relaunch fresh** instead, which starts a new conversation with its order as a wake does; Resume all skips it.
- **Resume never checks `max_ships`.** The engineer asked for these ships back. Resume all launches them one after another, and they count toward the cap for later autopilot launches like any ship underway.
- **A prompt only where silence would strand the ship.** A ship that was underway, and every Commodore, gets a fixed note: when the session was lost, that background tasks, Monitors and crew boats are gone, and which crew boats it had. A ship that needed you gets none, since a prompt would make it act. Like Return to port ([ADR 0036](0036-a-ship-returns-to-port-to-clean-up-before-it-ends.md)), the note says what happened, never how to recover; the skill owns its own crash-resume steps. Admiral does not relaunch crew boats.

## Consequences

- Supersedes [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md)'s "A dead tmux server ends every ship" and [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md)'s rule that a session removed without a dead pane gets no `ended`.
- Records start with this change, so a ship lost before it has no record and stays gone.
- A lost Commodore depends on its skill knowing its crash-resume steps; Admiral's note only triggers them.
