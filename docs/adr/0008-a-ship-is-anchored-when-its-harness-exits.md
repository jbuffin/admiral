# 8. A ship is anchored when its harness exits

Status: accepted

## Context

A ship needs a finished state, and Admiral needs to know what to do with it. Orders are freeform text, so most ships have no PR to watch, and no skill reports "done" in a way all three harnesses share. A finished turn already counts as needs-you (ADR 0007). Admiral runs each harness as the only process in its tmux pane (ADRs 0002 and 0003), so when the harness exits, nothing is left in the pane to type into. Finished ships left in open water would crowd out the live ones. The earlier name for this state, "docked", read as "back at the dock", which it never was.

## Decision

A ship is **anchored** once its harness process has exited, and only then. A merged PR doesn't count, and neither does a skill saying it's done. Admiral runs every pane with `remain-on-exit`, so the dead pane keeps its last screen and Admiral can read the exit status.

- **Exit 0:** the ship shows a green check, sails off and disappears, and Admiral removes its tmux session.
- **A non-zero exit:** the ship stays anchored where it is, still, with a red mark and the exit code on its plaque, until the engineer dismisses it. It survives Admiral restarts, because the dead pane lives in tmux.
- **Boarding an anchored ship** opens the bridge read-only on the frozen last screen and scrollback, with no prompt or key buttons and a **Dismiss** button. Dismiss kills the tmux session and removes the ship.
- **A bridge open when the harness exits** closes as the ship sails off on exit 0, and turns read-only with Dismiss on a non-zero exit.
- **Admiral has no way to end a live ship** in the MVP. The engineer ends one through the harness's own exit in the bridge (`/exit`, Ctrl-C, Ctrl-D).

Resuming an anchored ship's conversation is out of scope for the MVP.

## Considered options

- **The harness exits** (chosen). The same for every harness and skill, and tmux reports it.
- **The PR merges.** Most orders have no PR, and the MVP has no GitHub order source.
- **A skill or hook says done.** Nothing like this exists across all three harnesses.
- **Keep every finished ship until dismissed**, with or without a bulk clear or a holding area. A clean exit is one the engineer usually typed and just watched, so keeping it adds clutter for little gain. A crash is the case worth reading.

## Consequences

- Open water holds only live ships and crashed ones.
- A clean exit leaves nothing to read afterwards.
- A Ctrl-C that makes the harness exit non-zero leaves an anchored ship to dismiss.
- A ship that finishes its work but still waits at the prompt shows needs-you until the engineer exits the harness.
