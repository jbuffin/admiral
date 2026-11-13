# 25. Outside events are quarantined: trigger tiers, a Go-computed ceiling and a tool-less Lookout

Status: accepted, amended by [ADR 0035](0035-harness-features-follow-probed-capabilities.md) (A3 drops to A1 without a restricted-capable harness, Admiral picks the judge harness and models, and the advisor joins the judge calls)

## Context

The redesign lets work start without the engineer: a review requested on GitHub, a tag in Slack or Teams, an email, a monitor alert. A ship holds the engineer's repos and logins, so a stranger's text that steers it has private data, untrusted content and a way out together. The research in [#219](https://github.com/jbuffin/admiral/issues/219) ([note](../research/worker-guardrails.md)) found that comparable systems separate who may trigger work from what its text says, let deterministic code choose the action, and cut the way out. Grok Bot instead trusts a model to follow prose rules. [#224](https://github.com/jbuffin/admiral/issues/224) adopted the note's model, [#225](https://github.com/jbuffin/admiral/issues/225) decided standing orders and the reviewer, and [#226](https://github.com/jbuffin/admiral/issues/226) decided routines.

## Decision

**Who triggered an event is kept apart from what its text says. A Lookout with no tools reads the text. Go code, not a model, picks the action, and every term can only lower it.**

### Trigger tiers

Every outside event gets one tier, set by who caused it:

- **T0**: the engineer in Admiral. A scheduled routine run is T0.
- **T1**: the engineer's own pinned user ID in Slack or Teams, on a payload the channel can verify, or their own GitHub login on an event the built-in GitHub source saw itself. An event from a plugin is never verified.
- **T2**: allowlisted people and trackers, and the engineer's own email.
- **T3**: strangers.
- **T4**: monitor alerts.

The text is only ever data unless the tier is T0 or T1. Email never rises above T2, even with a secret phrase, because a From header can be forged. A chat message whose sender the channel can't prove is no higher than T2. Events written by Admiral's own ships or bots are ignored, so ships can't trigger each other in a loop. Sources supply the tier metadata (sender identity, allowlist match).

### The action ladder

A0 card only, A1 propose, A3 restricted ship ([ADR 0026](0026-untrusted-work-runs-on-a-restricted-ship.md)), A4 normal ship, A5 Commodore team.

There is no A2. #224 had A2 add an order, but the autopilot engine that launched added orders is retired and freeform orders are gone ([#240](https://github.com/jbuffin/admiral/issues/240), [#227](https://github.com/jbuffin/admiral/issues/227)), so an added order would only wait for a hand launch, as a proposal does. The engineer dropped it while this was recorded (2026-11-10). The other labels keep their numbers.

Go computes the effective action as `min(routine setting, tier ceiling, fleet ceiling, reviewer verdict)`. Each term can only lower it. T3 is hard-capped at A1 in every fleet, and no setting raises it. Where no harness in the fleet can run a restricted ship, A3 drops to A1 and never rises to A4, so untrusted text never reaches an unfenced ship.

### The Lookout

The Lookout reads the event. It has no tools and no network: on Claude Code, `claude -p` with `--tools ""`, `--strict-mcp-config`, `--json-schema` and a budget. It returns fixed fields only: kind, repo (picked from the fleet's search roots), severity, dedupe_key, suggested_action (from a fixed list) and a summary of at most 280 characters, marked tainted. It gets no brief and writes no ledger entries. An injected Lookout can at worst mislabel an event, which the ceilings bound.

Go then builds the ship's prompt from the routine's template. The raw event text goes in `.admiral/untrusted/event.md`, never on the command line ([ADR 0026](0026-untrusted-work-runs-on-a-restricted-ship.md)).

### Judge calls and their harness

The Lookout, the reviewer, the filing drafter and autopilot's advisor are judge calls: one-shot, tool-less calls Admiral makes for a decision. Admiral picks the harness: the first in the fleet with no tools and no network proven, in the order Claude, Codex, Copilot. It uses a model pair fixed per harness: a small model for the Lookout (Haiku on Claude) and a stronger one for the reviewer and the drafter (Sonnet on Claude). The Routines and standing orders screen shows the harness and models, with no override ([#241](https://github.com/jbuffin/admiral/issues/241)).

A missing capability turns off only the guard that needs it:

- **No tools and no network** are required. A fleet without a harness that has them runs no judge calls. It gets A1 proposals that Go builds from the source's metadata (title, sender, repo, link), a reviewer that fails closed whenever the fleet has Checks, and a plain form for filing.
- **No JSON schema** (Copilot): Admiral puts the schema in the prompt and checks the reply itself. A reply that doesn't fit counts as a failure: the Lookout drops to A1, the reviewer to `ask`.
- **No per-call cap** (Codex): that budget shows as not available. The daily cap holds where Admiral can count usage from the harness's token reports. Where it can't, the daily cap isn't offered and worker launches stay at Ask first.

Preferences and the Routines and standing orders screen say which feature is off and which capability is missing.

### Standing orders

The engineer writes standing orders in plain language, per fleet, on the **Routines and standing orders** screen. They apply only to work started by outside events. A hand launch, or AdmiralBot launching on a current instruction or from a commitment, is the engineer's own act, and so is a scheduled routine run. Nothing is shared between fleets; "copy to another fleet" is the only reuse.

- **Limit**: a field from a closed catalog that Go enforces, stored in the fleet's config ([ADR 0010](0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md)). The catalog: tier and fleet ceilings, sender allowlists, rates and caps, quiet hours, the repo allowlist, the restricted profile level, the wall clock, spend caps, the quota hold (hold unattended launches above N% of any plan window, 90% by default; [#246](https://github.com/jbuffin/admiral/issues/246)), path write-deny (Edit and Write deny rules plus a sandbox `denyWrite` on worker-launched ships), and label and sender skip filters. A Limit applies only once the engineer saves the confirmed field. The UI shows each as a sentence generated from its fields, linking to the field.
- **Check**: the engineer's own words, judged by the reviewer. At most 20 per fleet, about 300 characters each, always fleet-wide.
- **Guidance**: the engineer's own words, about 2,000 characters per fleet. Go puts it in the ship's prompt under a "Standing orders" heading, never in `.admiral/untrusted/`. Only worker-launched ships get it. Neither the Lookout nor the reviewer sees it.

**Filing.** "Add standing order" sends the prose to the drafter, which may split it and files each part as a Limit (with drafted field values, shown as setting changes plus the generated sentence), a Check or Guidance. Checks and Guidance keep the engineer's words. The engineer can change any kind before confirming, and the confirm step says so in plain words when a Limit loosens a setting. Filing is a T0 act and doesn't count toward worker spend. Prose that maps to no field can only be a Check or Guidance.

**Mute rule**, on a launch card or a Peek proposal, offers "Mute this sender for this routine" or "Mute this event" (by dedupe_key). Each creates a skip-filter Limit, listed with the others, removable, with no expiry.

AdmiralBot can read standing orders but can't add, change or remove one, not even to tighten it. Asked in the chat, it drafts the prose and links to the screen.

### The reviewer

- **When it runs**: only when the fleet has at least one Check and the provisional action, `min(routine, tier, fleet)`, is A3 or higher.
- **What it sees**: the Lookout's fields (the summary fenced and marked tainted), the routine, the proposed action and repo, and the fleet's Checks. Never the raw event or the brief. It has no tools.
- **What it returns**: `allow`, `ask` (drops to A1) or `block` (drops to A0, a card that names the Check), with the matching Check's id and a reason of at most 140 characters.
- **It fails closed.** An error, a timeout or a spent budget counts as `ask`.
- **It gates the launch only**, not tool calls during the run, which the restricted profile covers. Promote is a T0 act and skips it.

### Routines on outside events

A routine is the engineer's per-fleet rule: **When** (a schedule or a source event), **Do**, **Before acting** (Report only, Ask first or Act), an on/off switch and **Notify me**. A source event always goes through the Lookout and the ceiling above. It can never prompt AdmiralBot, which would put untrusted text in front of it. Events inside Admiral (a ship sailed off, a PR merged) are not routine triggers; they belong to ledger commitments ([ADR 0024](0024-admiralbot-memory-is-a-server-owned-ledger.md)).

Before acting maps onto the ladder: Report only is A0 (a card with the Lookout's summary), Ask first is A1 (an Inbox proposal), and Act is the computed ceiling.

A schedule has no outside text, so no Lookout, no reviewer and no standing orders. Its Do launches a ship from a stored launch spec or prompts AdmiralBot with a turn marked with the routine's name. A scheduled launch is a normal ship that can push. It waits for room under the global cap and for its resources, counts toward the fleet's launches-per-hour budget, and becomes Ask first under Hold fire. A missed run gets one late run on start if fewer than 12 hours have passed, otherwise Recent runs records it as missed. A scheduled launch carries the idempotency key `routine:<id>`, and a run is skipped while the last run's ship is live.

Every routine launch, scheduled or on an event, runs in autopilot mode ([ADR 0034](0034-autopilot-is-a-launch-mode-not-a-view-setting.md)), so a routine can only pick a dock whose harness carries autopilot mode.

Routines live in `$ADMIRAL_HOME/routines/<fleet>/routines.json`, edited through the UI and API, not by `admiral setup` and not in the fleet config. Before acting only ever lowers the ceiling, so it needs no config home. "Describe a routine" sends the prose to the drafter, and the engineer confirms. AdmiralBot can draft a routine and turn one off, but never saves, edits or turns one on.

### The trigger log

Each fleet has an append-only trigger log, `$ADMIRAL_HOME/routines/<fleet>/triggers.jsonl`, separate from the ledger. It holds one record per decision: event ref, tier, sender, rule matched, reviewer verdict and reason, action and ship. Each judge call records its cost in its record, and those costs make up the worker spend ([#246](https://github.com/jbuffin/admiral/issues/246)). Scheduled runs write a record too, at T0. A ship's `trigger` field, carried on its `launched` outcome record, points at its record. A routine's Recent runs are read from it.

### Where it shows

- A1 proposals are Inbox items: launch, dismiss or Mute rule from Peek. They expire after 24 hours unlaunched. A scheduled launch's proposal expires at the next run or after 24 hours, whichever is sooner.
- Launches (A3 to A5) post a card in the fleet's AdmiralBot chat with Stop, Undo, Promote and Mute rule. Undo stops the ship, removes its local branch and marks the trigger-log record.
- Notify me also pushes a routine's result to the engineer's devices.

### Budgets

Enforced in Go, with no model involved:

- launches per hour per fleet, and per sender per day
- a cap on concurrent worker-launched ships, counted inside the global cap
- one ship per dedupe_key
- a 60-minute wall clock per ship, after which Admiral stops it and it shows as anchored
- a per-call budget for each judge call (for example $0.25 for a Lookout and $0.10 for a reviewer), and one per-fleet daily **worker spend** cap covering the Lookout and the reviewer. Once it is spent, events stop at A1, and the screen says so.
- a circuit breaker: 3 undone or failed launches from a routine in a row drop it to Ask first, for scheduled and event routines alike
- quiet hours
- a Seer-style threshold for monitors

### Defaults

Every fleet starts on **Work**. **Home** is a "personal" preset the engineer can switch a fleet to on Preferences; Admiral can't know which fleet is home.

| Setting | Home | Work |
| --- | --- | --- |
| T1 ceiling | A5 Commodore | A4 normal ship; Commodore asks first |
| T2 ceiling | A3 restricted, Draft fix | A1 propose (decided by the engineer while recording, 2026-11-10) |
| T3 ceiling | A1 propose (hard cap) | A0 report only |
| T4 ceiling | A3 Draft fix, at 10 or more events in 14 days | A3 Investigate |
| Restricted ship can push | no | no |
| Network in a restricted ship | package registries | package registries |
| Launches per hour per fleet / per sender per day | 4 / 3 | 2 / 1 |
| Concurrent worker-launched ships | 3 | 2 |
| Ship wall clock | 60 min | 60 min |
| Lookout call / daily worker spend | $0.25 / $5 | $0.25 / $10 |
| Quiet hours | none until the engineer sets windows per fleet; held launches become proposals | none until the engineer sets windows per fleet; held launches become proposals |

### Hold fire

Per fleet and global. It can be turned on from the UI, `admiral hold`, a T1 Slack DM or AdmiralBot, and turned off only by the engineer in the UI or the CLI. It sets every routine to Propose and leaves running ships alone. A separate "Stop all worker-launched" ends those ships.

## Considered options

- **Tiers, a Go-computed ceiling and a tool-less reader** (chosen). An injected reader can only mislabel, and the label is bounded by code.
- **Prose rules a model follows**, as Grok Bot does. Simpler to write, but the engineer trusts the model to obey, and prompt injection is not solved.
- **One model that reads the event and acts.** It has private data, untrusted content and a way out at once.

## Consequences

- Amends [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md): `launched` gains a `trigger` field that points at the trigger-log record. The trigger log is a second append-only file beside the outcome log, and Admiral reads it back for Recent runs and the circuit breaker.
- Amends [ADR 0017](0017-autopilot-counts-retros-from-the-outcome-log.md): routine runs and the circuit breaker are counted from the trigger log, not the outcome log.
- Touches [ADR 0001](0001-local-web-app-served-from-the-mac.md) (T1 is a second way to know the engineer), [ADR 0006](0006-order-sources-are-subprocess-plugins.md) (sources supply tier metadata) and [ADR 0010](0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md) (allowlists and Limits live in the fleet config).
- AdmiralBot never sees an outside event's raw text: only cards built from the Lookout's fields.
- How Teams proves a sender is still channel plumbing, open on the map.
