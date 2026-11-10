# 34. Autopilot is a launch mode, not a view setting

Status: accepted, amended by [ADR 0035](0035-harness-features-follow-probed-capabilities.md) (the advisor is a judge call)

## Context

Today autopilot is an engine that launches a panel's orders without the engineer, within the panel's cap, the global cap and the panel's resources. It also adds retro orders after every N runs ([ADR 0017](0017-autopilot-counts-retros-from-the-outcome-log.md)) and resume orders when a stopped ship's items are answered ([ADR 0016](0016-answers-to-inbox-items-are-files-beside-the-item.md)). The engineer rarely uses it.

The redesign gives each of its jobs another home. Panels become views of a per-fleet source ([#227](https://github.com/jbuffin/admiral/issues/227)), and freeform orders are dropped. "Work through this" is an AdmiralBot ledger commitment, as campaigns are ([#229](https://github.com/jbuffin/admiral/issues/229)). "Launch when something new lands here" is a routine ([#226](https://github.com/jbuffin/admiral/issues/226)), and a routine on a source event puts the text through the Lookout and the trigger tiers ([ADR 0025](0025-outside-events-are-quarantined.md)), where the engine handed a stranger's issue text straight to a ship. Keeping the engine would leave two mechanisms for one job, one of them without the Lookout. Meanwhile every Commodore ship AdmiralBot launches already runs in autopilot mode ([skills#195](https://github.com/jbuffin/skills/issues/195)). [#240](https://github.com/jbuffin/admiral/issues/240) decided what is left.

## Decision

**The view autopilot engine is retired. "Autopilot" now means only the mode a ship is launched in when nobody is watching.**

- **What the mode is.** `ADMIRAL_AUTOPILOT=1` (the guard and the crew allowlist), `ADMIRAL_LOW_STAKES=advisor`, and Commodore's autopilot flow. It applies to plain-skill ships as well as Commodore ships. Low-stakes calls take the recommended default, and a high-stakes call becomes an Inbox question.
- **On by default for every unattended launch:** commitments, a campaign's Without-you children, scheduled and event routines, and review-wake rounds. Hand launches, and AdmiralBot launches on a current instruction the engineer marks "with me", stay interactive unless autopilot is ticked on the launch form.
- **Always `advisor`.** The `low_stakes: advisor | never` setting goes. A ship that should ask about everything is one launched outside autopilot mode.
- **The advisor is a judge call** on the fleet's judge harness, not Claude's `--advisor` flag ([ADR 0035](0035-harness-features-follow-probed-capabilities.md)).
- **Only on a proven harness.** Autopilot mode runs on a harness once its guard, args allowlist and advisor are proven on this machine ([ADR 0035](0035-harness-features-follow-probed-capabilities.md)). A routine, commitment or campaign can only pick a dock whose harness carries autopilot mode, and drafting refuses any other. If the capability is lost later, the launch becomes an Inbox item ("couldn't launch unattended on Codex: guard not proven") and never runs unguarded.
- **No auto-merge anywhere.** No launch path offers it, and the guard refuses every merge from an autopilot-mode ship, including `gh pr merge --auto`. Merging stays the engineer's click in Peek or My PRs. If auto-merge is ever wanted, it comes back as a per-fleet Limit that AdmiralBot can't turn on.
- **Views get no autopilot setting.** A view has an **Automate…** action that opens a drafted routine ("When a new item lands in this view, launch Commodore") for the engineer to confirm, like any routine.

What happens to the engine's parts:

- **Global cap:** stays as a machine limit. It moves out of `autopilot` to a top-level `max_ships` (default 3) and counts every ship underway or needing you.
- **Resources:** stay top-level. The launch names them (a routine's Do, a commitment or the launch form) instead of a panel entry. Hand launches still hold none unless one is named.
- **Per-panel `max_ships`:** removed, with no per-routine replacement. The global cap, the per-fleet cap on concurrent worker-launched ships and the fleet's launches-per-hour budget ([ADR 0025](0025-outside-events-are-quarantined.md)) already throttle a burst. A per-routine cap can come later as a Limit if one routine starves another.
- **Retro orders:** removed (`retro_every`, `retro_queued` and the counter). Periodic retros are a scheduled routine, a T0 act.
- **Resume orders and `resume_source`:** removed. Answering a stopped ship's items wakes the same ship at its prompt ([#199](https://github.com/jbuffin/admiral/issues/199), skills#195), and a moored ship keeps its `resume` text for waking ([#230](https://github.com/jbuffin/admiral/issues/230)). With freeform dropped, there is no panel left to add an order to.
- **Panel pause:** removed. A routine's on/off switch and the fleet's Hold fire cover it.
- **Launch args:** a routine's Do launch takes the launch form's fields (harness, model, effort, skill and resources). Extra args saved from the UI, or drafted by AdmiralBot, must pass `launcher.AutopilotArgs` against the fleet dock's args. Hand edits stay unchecked.

**Migration.** On upgrade, `autopilot.max_ships` moves to the top-level `max_ships`, and `resume_source` and every panel entry are dropped. Admiral posts one Inbox note that lists each entry that was enabled, each with an **Automate…** link to its drafted routine. Nothing becomes a routine without the engineer confirming it.

## Considered options

- **Retire the engine and keep autopilot as a launch mode** (chosen). One mechanism per job, and outside text always meets the Lookout.
- **Keep autopilot as a view setting beside routines and commitments.** Two ways to launch a view's items unattended, one of which skips the Lookout and the tiers.

## Consequences

- Supersedes [ADR 0017](0017-autopilot-counts-retros-from-the-outcome-log.md). Nothing counts runs from the outcome log any more, so [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md) loses its one exception: nothing reads the log back to decide anything. Its `usage` records are read only to show totals ([ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md)'s 2026-11-10 amendment).
- Removes the resume-order half of [ADR 0016](0016-answers-to-inbox-items-are-files-beside-the-item.md): `resume_source`, `resume_orders` added as orders and the origin-panel bookkeeping go. Typing a live ship's resume at its prompt stays.
- Amends [ADR 0022](0022-a-ui-save-of-autopilot-args-passes-the-crew-allowlist.md): the args checked are a routine's launch args, not a panel's autopilot args. The rule is otherwise unchanged.
- Amends [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md): autopilot mode is the default for every unattended launch, not only Commodore launches from AdmiralBot, and the guard refuses all merges.
- A quota hold ([#246](https://github.com/jbuffin/admiral/issues/246)) drops unattended launches, autopilot ones included, to Propose while a plan window is above its line.
- An existing autopilot config loses its panel entries on upgrade. The engineer gets them back only by confirming each drafted routine.
