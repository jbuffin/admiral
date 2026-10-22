# 15. Outcomes are an append-only history beside tmux

Status: accepted

## Context

Autopilot (rung 1 is [#113](https://github.com/jbuffin/admiral/issues/113)) needs to know how ships went: which orders ended, how often a ship needed the engineer, and why. ADR 0012 keeps a ship only in its tmux session, so a ship that is dismissed or sails off leaves nothing behind, and a retro has nothing to read. The history has to survive the ships it describes without becoming a second record of the ships that are alive.

## Decision

**tmux stays the only record of a live ship (ADR 0012).** The outcome log is a history beside it. Admiral appends to it as events happen and never reads it back, not to rebuild ships and not to decide anything.

- **One file per UTC month,** `$ADMIRAL_HOME/outcomes/<YYYY-MM>.jsonl`, named for the month of the record's `ts`. One JSON object per line with `v` (1), `ts` (RFC 3339, UTC), `ship` (the session name) and `event`, plus the event's own fields. Nothing is rotated or pruned.
- **Five events.** `launched` (fleet, harness, skill, order source, panel, order id and URL, title, directory, `autopilot`), `needs_you` (a `reason`), `underway` (`waited_s`), `ended` and `dismissed`. An `underway` record means a needs-you stop cleared, not that the ship's status is underway. `ended` carries `exit_code` or `signal`, `duration_s`, `needs_you_count`, `needs_you_s` and the ship's `report`.
- **One writer goroutine, `O_APPEND`.** Records are queued and written in order, the file opened for each one. A write failure is logged once until a write succeeds, and never blocks a launch, a hook or a status change. The queue holds 1024 records; one that finds it full is dropped, and the first drop is logged.
- **A needs-you stop is one stop** whatever its reason does while it lasts. Admiral compares each ship's status with the last pass it saw: moving into needs-you writes `needs_you`, moving out to underway writes `underway`. A stop that the pane's death ends gets no `underway`.
- **Reasons** come from `@admiral-reason` (ADR 0007), kept only if they match `^[A-Za-z0-9_.:-]{1,64}$`. A needs-you state with no usable reason is `unknown`. `idle` means no hook within 10 seconds of launch (`NoHookWait`, ADR 0007), so a ship that has never spoken. A dock launch with no text starts the harness idle (GLOSSARY.md, Launch orders), so it will usually stop with this reason.
- **`ended` is written once.** When a pane is dead and the session has no `@admiral-ended` option, Admiral queues `ended`; once it is written, Admiral sets the option. The log is never read to find out. A start-up writes `ended` for every ship that died while Admiral was stopped, before it kills an exit-0 ship. `duration_s` runs from `@admiral-launched` to the pane's death time (tmux's one-second resolution).
- **A ship's report and inbox go after its `ended` is written.** `ended` carries `$ADMIRAL_REPORT` as the ship left it: JSON, at most 1 MiB, read without following a link; otherwise `null`, with a line in Admiral's log. A ship that was dismissed or sailed off has `reports/<session>.json` and `inbox/<session>/` removed once the writer has written its `ended`. If the write fails, the files stay and the next start retries the `ended`. `dismissed` is queued behind the `ended` Dismiss has just caught up on, so the two are in that order in the log. A name that a newer ship has taken keeps its files.
- **Counters live in tmux too.** The count of needs-you stops, their total seconds and the start of the open stop are session options: `@admiral-needs-count`, `@admiral-needs-s` and `@admiral-stop-since`. A restart reads them back, so a stop that was open stays one stop and `ended` still has the totals.
- **Ships from before this change** have no `launched` record. They get `ended` when they end, with counters from the time Admiral first sees them.
- **`admiral outcomes [--since <date>] [--ship <name>]`** prints the stored lines for retros and scripts (`docs/cli.md`). It reads the files directly, so it works with the server stopped.
- **`$ADMIRAL_HOME` also holds** `outcomes/`, the log; `reports/`, one `<session>.json` a ship writes; and `inbox/`, one directory per ship (`$ADMIRAL_INBOX`). Admiral creates `reports/` and the ship's inbox before the harness starts.

## Considered options

- **An append-only file beside tmux** (chosen). Nothing to keep consistent with the sessions, cheap to write, and `grep` and `jq` read it.
- **Keep the history in tmux session options.** A session's options go with the session, and that is the moment the history is for.
- **SQLite or a table in `state.json`.** The same objections as ADR 0012 gave: a dependency and a second record that can disagree with tmux, for lines nobody queries yet.
- **Read the log back to rebuild a ship or to decide `ended`.** It would put a second source of truth beside tmux and make a damaged file a startup problem.

## Consequences

- A retro can read what each ship did without Admiral running, and `ended` carries what the ship reported.
- The log is a history, not a ledger. Records can be lost: a full queue drops them, and a record whose write fails is not tried again. Only a missing `ended` is: the next start writes it for a dead pane that has no marker.
- **A ship can skew its own record.** `@admiral-ended` and the counter options are session options, and any process in the ship's pane can write them. A ship can suppress its `ended`, or skew its counters and `needs_you_s`. A value that does not parse is dropped (a count of 0, no open stop). This matters once autopilot reads the log: treat what a ship put in `report` and these options as the ship's own word.
- **Accepted gaps.**
  - A session removed without a dead pane (an external `tmux kill-session`, `kill-server`, a reboot) gets no `ended`, and its report and inbox stay.
  - A ship Admiral kills (sail-off, dismiss, start-up) while its `ended` write is failing loses that `ended` and keeps its files. Sail-off and Dismiss never wait on the log.
  - `waited_s` and `needs_you_s` include Admiral's downtime for a stop that ended while Admiral was stopped, unlike `duration_s`, which uses the pane's death time.
  - `needs_you_count` can exceed the `needs_you` lines for a ship launched before this change: its open stop counted as one with no record.
  - A line cut short by a crash stays in the file. `admiral outcomes` skips it, as it skips any line that is not a JSON object with a time.
- `admiral outcomes` prints each stored line as it is. A record's strings are escaped by JSON, so a control character cannot reach the terminal from them, but a `report` is the ship's own JSON, and it may hold characters JSON does not escape, such as bidirectional or C1 controls. Pipe the lines through `jq` when that matters.
