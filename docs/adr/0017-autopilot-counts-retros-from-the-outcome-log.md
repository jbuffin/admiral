# 17. Autopilot counts retros from the outcome log

Status: accepted

## Context

Retro orders (rung 5 of autopilot, [#117](https://github.com/jbuffin/admiral/issues/117)) add a `/retro` order to the resume panel after every N autopilot runs of a panel. The only record of a finished run is the outcome log, and ADR 0015 says Admiral "never reads it back, not to rebuild ships and not to decide anything". Counting runs in memory would lose them at a restart. Keeping a counter in `state.json` or `autopilot.json` would make a second record that can disagree with the log, which is the objection ADR 0015 made to a second source of truth.

## Decision

**Autopilot reads the outcome log for one purpose: counting runs toward retro orders.** Nothing else reads it back, and tmux stays the only record of a live ship (ADR 0012). The counter keeps no state of its own, so a restart counts what a live pass does.

- **A counted run** for panel `(source, panel)` is an `ended` record that joins to the latest earlier `launched` record with the same `ship`, where that `launched` has `autopilot: true` and the panel's `source` and `panel`, whatever the exit. Earlier means earlier in the files read in name order, line by line, never by `ts`. A `launched` joins one `ended` at most, so the second `ended` ADR 0015 allows for a ship counts for nothing. An `ended` with no earlier `launched` counts for nothing.
- **A retro resets the count.** Only runs after the panel's last `retro_queued` count. The record is ship-less and carries `source`, `panel`, `since`, `text`, `resume_source`, `resume_panel`, and `order_id` or, when the add may have landed, `uncertain: true`.
- **A retro ship counts toward no panel.** A `launched` whose `source` and `panel` are a `retro_queued`'s `resume_source` and `resume_panel` and whose `order_id` is its `order_id` is a retro ship; for an uncertain record, which has no id, the match is a `title` equal to the record's `text`. A record with neither an `order_id` nor a `text` matches nothing. One rule matches an order, a ship or a `launched` to a record.
- **Only records for the configured resume target count.** A `retro_queued` whose `resume_source` and `resume_panel` are not the current `autopilot.resume_source` is ignored, both for the count and for what is pending. Admiral writes a record only for the target it added the order to, so any other is a forgery or a retro queued before the engineer changed the target. Changing `resume_source` therefore drops the hold on the retros queued to the old target, and can add one extra retro.
- **The order** is `/retro <source>/<panel> --since <ts>`, added to the resume panel. `ts` is the previous `retro_queued`'s `ts`, or with none, the `launched` `ts` of the first counted run. It is parsed as RFC 3339 and written again in UTC, never copied, so a forged `ts` cannot put text into the order. A panel id with whitespace or a control character gets no retro.
- **One at a time.** While a panel has a retro pending, no second is added. A retro is pending when its record is not confirmed written, or when any of the panel's records has its order in the resume panel's listing, or that source is configured and has not listed, or a ship that has the order exists, in any status. Every record of the panel is checked, not only the last, so a forged record cannot clear the real one. The log carries all but the first condition over a restart. While the ship list is not known (before the host's first read, or while tmux is lost), nothing is added for any panel.
- **The record is the reset, so it must be written.** The panel is pending until `retro_queued` is confirmed written. A write that fails or is dropped is logged once and written again; the add is not repeated. The unconfirmed writes are noted before the log is read, so a write confirmed during the read still holds the panel.
- **The reader** decodes each line into the few keys the count uses (`report` is never held) and skips anything under `outcomes/` that is not a regular file, so a FIFO cannot hang a pass.
- **Errors.** With no `autopilot.resume_source`, autopilot warns once and adds nothing. A retryable or final refusal is logged once and the retro is tried again on a later pass; the count stays in the log, so waiting costs nothing. This differs from a resume order, which is dropped on a final refusal. An uncertain result writes the uncertain record, so the same retro is never added twice.
- **`retro_every`** is a per-panel autopilot key: an integer of 0 or more, default 5, with 0 turning retros off.

## Considered options

- **Count from the log** (chosen). One record, no new state, and a restart and a live pass agree because they read the same lines.
- **A counter in `autopilot.json`.** It would have to be written with every run and agree with the log and with tmux after a crash.
- **A count in memory, rebuilt at start.** Rebuilding is reading the log, with a second path to keep in step.

## Consequences

- The log can now change what Admiral does, so a damaged or forged line matters more than it did. The cost is bounded: a wrong count skips a retro or adds an extra one, and the order text takes nothing from the log but a reformatted time.
- **Accepted gaps.**
  - Records lost to a full queue or an unwritable `outcomes/` (ADR 0015) are not counted. A lost `launched` or `ended` costs a run; a lost `retro_queued` write is retried, but one lost to a crash is not.
  - A duplicate `ended` that Admiral writes for a ship (ADR 0015) counts once. A ship name that is reused joins its latest `launched`.
  - A ship can append forged lines (ADR 0015). Forged runs, or a forged `retro_queued` for the configured target, bring a retro early, and can do so again each time one clears, but at most one retro is pending per panel. A forged record that names a real, long-lived order holds the panel while that order lasts. A forged `ts` cannot inject order text.
  - A run launched before a `retro_queued` and ended after it counts toward the next retro, but its `launched` `ts` is earlier than that retro's `--since`.
  - A crash between a successful add and the `retro_queued` write leaves the count unchanged, so the next pass adds one extra retro.
  - Admiral reads every month file on a pass that has a panel with retros on, so a large log costs time on each pass, though the memory it holds is only the few keys of each line.
  - The add runs under the engine's pass lock, as a launch does, so a slow resume plugin delays a pass by up to 10 s per due panel.
