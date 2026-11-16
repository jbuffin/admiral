# 39. A ship is handed to Commodore in place, resuming its conversation

Status: accepted

## Context

A ship that has settled a design with the engineer holds the context a Commodore needs to turn that design into work, and a fresh Commodore would have to be told it all again. The engineer wants to hand the ship to Commodore without losing its name, checkout or conversation.

[ADR 0028](0028-a-ship-with-an-open-pr-moors-and-relaunches-for-each-feedback-round.md) relaunches a moored ship under its own name for each feedback round, and rejected "Resume the harness conversation" because ids differ per harness and the context comes back bloated. That is the right call for a round: the work is small, the PR carries its state, and a fresh conversation is cheap. A hand-off is the opposite case. The conversation is the thing being handed over, and the ship is live, so its context is as large as the engineer chose to let it grow. [ADR 0037](0037-a-lost-ship-is-recorded-and-resumable.md) also wants the harness's resume flag for a lost ship and has not built it.

## Decision

**Hand to Commodore stops a live Claude ship and relaunches it under the same name and checkout, resuming its conversation with `--resume`, as a Commodore that starts from a ref the ship wrote.**

- **Claude only.** The relaunch passes `--resume <id>`, using the session id Admiral recorded when it launched the ship with `--session-id`. Codex and Copilot ships show the action disabled with a reason, and a follow-up issue covers them.
- **The engineer chooses the hand-off.** In `/app/` they pick Hand to Commodore and choose a GitHub issue (recommended) or Other (free text) in a sheet. Admiral types a prompt into the live ship, the way Return to port does. The prompt asks the ship to file the settled design as an issue in its repo (or comment on its launch issue), or to follow the Other text, and to write the issue URL or a brief path to a handoff file Admiral names.
- **The ship's text is checked before it reaches a launch.** At the end of the turn Admiral reads the handoff file and accepts only an issue URL in the ship's repo or a path under the ship's directory. It then stops the harness and relaunches with the same name and checkout, `--resume`, Commodore's env and the prompt `/commodore <ref>`.
- **The relaunch is attended.** It sets no `ADMIRAL_AUTOPILOT`. Eligible ships are live, aren't Commodore, aren't restricted, weren't launched from an outside event, and run on Claude. It counts like a hand Wake: counted against `max_ships`, never blocked by it.
- **The launcher takes the conversation id as a request field only the server sets.** It refuses the launch unless the harness is `claude`, the id is a lowercase version-4 UUID, and neither the dock nor the extra args choose a session or carry `--fork-session`. The pane's `transcript` option names the file a `--session-id` launch would have, so the conversation view, usage and moored rounds bind a resumed ship as they bind any other. Binding accepts `--resume <id>` from a Claude start command only; Copilot stays as it is.

## Consequences

- This sits beside ADR 0028 and changes none of its decisions: a moored round still starts fresh. Only a hand-off resumes, because the engineer asked for the conversation.
- The id is the one the ship launched with. After a `/clear` the resume returns to the conversation from before it. ADR 0037 records the changed id for a launch record, and a hand-off can follow once that exists.
- ADR 0037's Resume can reuse the same launcher field and the same binding.
- A Commodore resumed this way starts with the ship's full context, which can be large; the engineer sees that cost only when the hand-off is made.
- Codex and Copilot ships can't be handed over until their resume is built.
