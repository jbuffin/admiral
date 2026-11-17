# 45. A fleet digest is a routine action that cannot act

Status: accepted

## Context

The map [AdmiralBot as a personal assistant](https://github.com/jbuffin/admiral/issues/653) set Claude Duet's morning brief and Grok Bot's primary Bot reporting on every Bot as the bar. [ADR 0042](0042-a-ships-result-comes-back-as-a-card-and-a-report-turn.md) reports each ship AdmiralBot launched from the chat, but nothing sums up what a fleet did overnight. [#711](https://github.com/jbuffin/admiral/issues/711) asked whether AdmiralBot should post such a digest, when, and what goes in it.

A scheduled routine can already send AdmiralBot a prompt, typed into the chat as `[Routine: <name>] …`. The turn gate ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)) counts that as an engineer turn, so AdmiralBot may act in it, and only the prompt's wording would keep a summary from launching ships. Report only on such a routine sends nothing at all. Separately, [ADR 0024](0024-admiralbot-memory-is-a-server-owned-ledger.md)'s "since your last turn" digest prepends events to the engineer's next message, on Claude Code alone.

## Decision

**A fleet digest is a third routine action, Post a fleet digest, beside launching a ship and sending AdmiralBot a prompt. Admiral gathers the facts; AdmiralBot writes a short summary from them and can't act while it does. The next-message digest is removed.**

- **A routine, not a new feature.** The engineer adds it on the Routines screen or through the drafter, like any scheduled routine: one fleet, a time they choose, off until added, held by Hold fire, logged in the trigger log. It takes optional extra words from the engineer ("lead with PRs"). It runs on a schedule, never on a source event.
- **It can't act.** The kind of action, not a setting or the prompt, makes the digest turn read-and-talk only: the turn gate refuses every AdmiralBot write during it, as during a report turn. AdmiralBot may offer follow-up work ("Want me to wake the anchored one?") and acts only on the engineer's answer.
- **The fact sheet.** Admiral puts in the turn what changed in the fleet since the last digest that ran: ships that sailed off, anchored or were lost; ships moored with a PR, marking Ready to merge and merge problems; the Inbox's count and its oldest item; campaign children that closed or became ready; routine runs and proposals. A section with nothing in it is left out. When every section is empty, no turn runs and the trigger log records "nothing new".
- **AdmiralBot writes it.** A few lines with links, from the fact sheet, without repeating a report it already gave. If the turn fails, the cards already posted still hold every fact.
- **One fleet.** The fact sheet reads only the routine's fleet, and the digest posts only in that fleet's chat. Nothing summarizes across fleets.
- **Delivery.** The digest turn joins the fleet's report-turn queue from ADR 0042 and is typed into the chat when it is idle; a reset waits for it. A run missed while the Mac slept follows the routine's missed-run rule, and the next digest covers the whole gap, since its window starts at the last digest that ran.
- **Pushes.** AdmiralBot's digest reply pushes like a report under [ADR 0033](0033-pushes-are-web-push-from-the-mac-with-no-hosted-relay.md): not while the engineer is watching, opening the chat at that message, outside the attention count.
- **The next-message digest goes.** Cards and report turns tell AdmiralBot about the ships it launched, the brief it reloads lists live ships, ships waiting on the engineer and open commitments, and the fleet digest covers the rest.

## Considered options

- **A routine action that can't act** (chosen). Schedule, Hold fire, the trigger log and the drafter already exist, and the no-writes rule can't be loosened by a setting.
- **A built-in digest with its own schedule setting.** Duplicates routines.
- **A prompt routine with a "talk only" flag, or Report only redefined to mean it.** Any prompt routine could then be made read-only, but a setting could also loosen a digest, and Report only would mean two things.
- **A card Admiral builds from data, with no model turn.** Cheap and reliable, but no summary through AdmiralBot, which is the bar this map set.
- **No digest.** Cards and report turns cover single ships, but nothing sums up a fleet the engineer hasn't watched.

## Consequences

- Amends [ADR 0024](0024-admiralbot-memory-is-a-server-owned-ledger.md): the "since your last turn" digest and its `UserPromptSubmit` hook are removed, with the commitment card's digest line.
- Amends [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md): a digest turn, like a report turn, is not an engineer turn.
- Amends [ADR 0042](0042-a-ships-result-comes-back-as-a-card-and-a-report-turn.md): the report-turn queue also carries digest turns.
- A digest is a model turn per fleet per run, skipped when nothing changed.
