# 27. Campaigns are read from GitHub's issue tree; Admiral never writes to it

Status: accepted

## Context

A campaign is a parent issue with sub-issues, such as a wayfinder map or a spec, that Admiral follows as one effort ([#211](https://github.com/jbuffin/admiral/issues/211)). Blocks are strict, and tickets that need the engineer start one at a time. The research in [#216](https://github.com/jbuffin/admiral/issues/216) ([note](../research/campaigns-issue-trees.md)) found that GitHub's GraphQL API gives the tree, its blocks and its order. [#229](https://github.com/jbuffin/admiral/issues/229) decided how Admiral reads it, which children may start, and who moves the campaign forward.

The obvious way to stop two sessions taking the same child is to claim it by assigning it on GitHub. The skills ships run (wayfinder, Commodore) already claim, close and file tickets themselves, so Admiral writing to the same tree would race them.

## Decision

**A campaign is a GitHub issue tree that Admiral reads, shows and launches from, and never writes to. AdmiralBot launches the campaign's children and keeps it moving.**

- **GitHub only.** Trees come from the fleet's built-in GitHub client ([ADR 0006](0006-order-sources-are-subprocess-plugins.md)), and the whole tree routes to one fleet like any GitHub item: project, then owner, then default fleet.
- **Never writes.** Admiral assigns, labels, closes and creates nothing in the tree. Its claim on a child is server-side only ([#222](https://github.com/jbuffin/admiral/issues/222)). Claiming on GitHub, closing tickets and graduating fog belong to the skill the ship runs. When AdmiralBot lays out a campaign, it launches a ship that runs the wayfinder skill to chart the map.
- **Ready.** A child is ready when it is open, every blocker is closed (blockers outside the campaign included), nobody is assigned to it, and no live ship holds Admiral's claim on it. Ready children keep sub-issue order.
  - A child assigned to someone with no live ship shows as "claimed, no ship", with *Start anyway*. Admiral can't tell a session outside Admiral from a stale claim, so it doesn't guess.
  - Nested parents (a sub-map, or a spec child with its own children) fold into the tree and are never launched. Their ready children join the campaign's ready set in tree order. A blocked parent blocks everything under it.
  - When a child isn't a sub-issue (past GitHub's 100-sub-issue limit), the reader also takes `Part of #n` and `Blocked by:` lines from issue bodies.
- **With you or Without you.** Each child has a default mode and starting skill, which the engineer can override per campaign when following it or later:

  | Child | Mode | Ship starts on |
  | --- | --- | --- |
  | `wayfinder:grilling`, `wayfinder:prototype` | With you | `/wayfinder <map> <ticket>` |
  | `wayfinder:research` | Without you (several may run at once) | `/wayfinder <map> <ticket>` |
  | `wayfinder:task` | Without you, or With you if `needs-human-action` | `/wayfinder` |
  | plain child, `ready-for-agent` | Without you | Commodore |
  | plain child, `ready-for-human` / `needs-triage` | With you | none suggested |

- **With-you children go one at a time per fleet.** AdmiralBot proposes the next one only when no ship it started on a With-you campaign child in that fleet is underway or needing you. Starting one by hand is never blocked. The proposal is a chat question; on yes, AdmiralBot launches the ship and replies with a Board link, and nothing navigates on its own. Ready With-you children are not Inbox items.
- **Working a campaign.** "Work on #n" to AdmiralBot follows the campaign and records one ledger commitment (ADR 0024). It launches each Without-you child as it becomes ready, as an unattended launch that waits for room under the global cap and for any resources it names. It asks a chat question for each With-you child, under the per-fleet limit. It ends when the campaign ends. Autopilot plays no part ([#240](https://github.com/jbuffin/admiral/issues/240)).
- **Ships.** One ship per child by default. A **slice** is several ready Without-you plain children launched as one Commodore ship, built as a gh stack. That ship holds the claim on every child in the slice and stays with the change through review ([ADR 0028](0028-a-ship-with-an-open-pr-moors-and-relaunches-for-each-feedback-round.md)). A slice is formed only by the engineer's hand or on their instruction, never from a commitment alone.
- **Cross-repo.** The whole tree belongs to the campaign's fleet. Every child launches in that fleet and its repo is cloned if missing. There is no extra guard for a child whose repo routes to another fleet; the routing warning for a repo two fleets claim covers it. A campaign may name a **work repo**, the default checkout for children filed in the parent's repo (a map in a docs repo, its build in the code repo).
- **Following.** By hand: **Follow campaign** on any issue with sub-issues, wherever an order shows (a view, Peek), or by pasting an issue URL into the Campaigns group. Following by hand only shows the campaign; nothing launches until asked. Through AdmiralBot: "work on #n" follows it and records the commitment.
- **Refresh.** Admiral rereads the tree with GraphQL when a ship on a child sails off or anchors, when the campaign page opens, and every 5 minutes per followed campaign. The commitment acts on the latest read.
- **End.** A campaign ends when the parent and every child are closed, or at once when the engineer stops following it. A closed parent with open children keeps going until they close. Closed children under an open parent keep going, because fog may graduate into new tickets. An ended campaign leaves the sidebar, its commitment ends, and it stays readable under an "Ended" filter.

## Considered options

- **Read only, with a server-side claim** (chosen). The skill on the ship owns the tree, so Admiral never races it, and a crashed Admiral leaves nothing stale on GitHub.
- **Claim by assigning the child on GitHub.** Visible to everyone, but Admiral would write to a tree its ships' skills also write to, and an assignment left behind by a crash looks like real work.
- **Campaigns from the work tracker too, through a plugin `tree` capability** (`campaigns.list`, `campaigns.tree`, `startable`). Out of scope for now: the tracker's readiness is per workflow and its CLI can't draw blocks ([#228](https://github.com/jbuffin/admiral/issues/228)).

## Consequences

- A child someone assigned on GitHub is never ready, even when nobody is working on it. "Claimed, no ship" and *Start anyway* are the way through.
- Admiral's claims live only on the server, so two Admirals on different machines can start the same child.
- Amends [ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md): a campaign's work repo comes first in the checkout preselect, before the order's repo.
- Amends ADR 0023: AdmiralBot reads campaigns, and "work on a campaign" is a ledger commitment with the rules above.
- Amends ADR 0023, from [skills#195](https://github.com/jbuffin/skills/issues/195): the launch tool gains `issues` (two or more make a slice; Admiral claims every child, and the idempotency key is the sorted URL set) and `crew` (members pinned by name). Commodore ships AdmiralBot launches run in autopilot mode by default, with no auto-merge.
