# Issue trees in Admiral: campaigns

> Research for the Admiral redesign map, 2026-11-09. Decided since: "campaign" is the word. Campaigns start by hand (or AdmiralBot brings one in or proposes one); auto-discovery may come later, and then only for parents assigned to the engineer. GitHub blocks are strict. With-you tickets start one at a time, throttled to the engineer's attention, and only tickets already underway reach the inbox.

## What the owner's trees look like today

The owner's own projects (private repos, looked at read-only) show both shapes:

- **Wayfinder maps**: a map issue with 9 to 13 children, about half done. Every open child carries `wayfinder:<type>`, and most of them are `grilling`. Blocking uses native dependencies, and some blockers sit outside the map. Beside the maps sit plain trees: a spec with 69 children, some of which have children of their own. Readiness on those children comes from triage labels (`ready-for-agent`, `ready-for-human`, `needs-triage`), and a milestone label is the "tag" workaround.
- **A cross-repo map**: the map lives in a *docs* repo, but its build tasks run in the *code* repo. It has 91 sub-issues and a nested map. Its newest children are linked only by a `Part of #<map>` line in the body, not as sub-issues. Tickets that need a person carry `needs-human-action`.

So a tree is the shape of most of the owner's work, but Admiral only sees a flat search per panel. My tests (GraphQL `ISSUE_ADVANCED`) also show that `parent-issue:` finds nothing, while `is:blocked` and `has:parent-issue` work. A panel can't ask GitHub "the ready children of #230", which explains why this takes labels today.

## Recommendation: a first-class **campaign**

**Campaign**: a parent issue that Admiral follows as one effort, with its tree of child orders, read live from the tracker. A wayfinder map is a campaign, and so is a spec with sub-issues. The word is naval (a series of operations toward one objective), it clashes with nothing in GLOSSARY.md, and it covers both kinds. "Chart" fits wayfinder better, but a 69-child build spec is not a chart. Wayfinder's words (destination, frontier, fog) stay on the tracker and in the skill. In Admiral the frontier is the campaign's **ready** orders.

A campaign belongs to one fleet, through the order source it comes from. It is not:

- **a project**: a project is *where* code lives (repos and checkouts). A campaign is *what* the engineer is trying to reach, and it can span repos (the cross-repo map above).
- **a label**: Admiral shows labels and never sets them. Following a campaign needs no label on any issue. `wayfinder:map` only helps Admiral discover campaigns.
- **a panel**: a panel is a saved search, flat. A campaign is a tree with an order and blocking edges.

Admiral never edits the tree. Claiming, closing and graduating fog stay with the skill a ship runs, as wayfinder already does. Admiral reads, shows and launches.

### Which children are ready, and who works them

A child is **ready** when it is open, every blocker is closed (including blockers outside the campaign), nobody else is assigned to it, and no live ship holds it. Ready children keep map order (sub-issue priority).

Each ready child gets a **crew mode** from rules the campaign keeps:

| Child | Mode | Starts on |
| --- | --- | --- |
| `wayfinder:grilling`, `wayfinder:prototype` | **with you** (HITL) | `/wayfinder <map> <ticket>` |
| `wayfinder:research` | **AFK** | `/wayfinder <map> <ticket>` (research may run in parallel) |
| `wayfinder:task` | AFK, or with you if `needs-human-action` | `/wayfinder` |
| plain child, `ready-for-agent` | AFK | Commodore |
| plain child, `ready-for-human` or `needs-triage` | with you | none suggested |

The defaults cover the wayfinder labels and the triage vocabulary. The engineer can change the rules per campaign, and a tracker plugin can state the mode itself.

**One ship per child**, following wayfinder's own rule that one session resolves one ticket. A ship for the whole map is wrong: a map is by definition more than one session can hold, and its crew boats would die with it. The exception is Commodore. The engineer can multi-select a ready slice of plain build children, such as a run of six build issues from one ADR, and launch it as **one ship** whose crew builds it as a gh stack. That ship then stays with the change through its review rounds, as the redesign already decides.

**Autopilot on a campaign** launches only AFK ready children, under the existing caps and resources. It never launches a "with you" child. A HITL ticket started with nobody there would only sit as *needs you*, and wayfinder forbids the agent answering for the human.

### AdmiralBot and Commodore

The fleet's AdmiralBot reads campaigns through the CLI (`admiral campaign list` and `admiral campaign show <id>`, which gives the tree, ready set and modes). It drives a campaign this way:

1. When a ship that worked a child sails off, it re-reads the tree: new tickets have graduated out of fog, and blockers have closed.
2. It launches newly ready AFK children (research, agent tasks), or hands a build slice to Commodore.
3. For a ready grilling ticket it asks a **chat question** ("Grilling: what figures do the ships get… is ready. Start it now?"), and on yes it launches a with-you ship and points the engineer at its bridge.

Steps 2 and 3 need AdmiralBot to launch ships, which changes ADR 0020.

### UI sketch

```text
 WORK FLEET                 │  Map: make milestone 1 playable                    owner/game#230
 ▸ Inbox              3     │  Destination: milestone 1 playable end to end        ██████░░░░░░ 6/13
 ▸ Ships              4     │  Ready 3 · With you 2 · Blocked 4 · Underway 1          [Autopilot AFK ▢]
 ▾ Campaigns                │ ─────────────────────────────────────────────────────────────────────
   ◐ Milestone 1    6/13 ●2 │  ◉ Grilling  what figures do the ships get…       with you   [Start]
   ◐ Visual pass    4/9     │  ◉ Grilling  how do systems map onto slots…       with you   [Start]
   ◐ Spec: M1       55/69   │  ◉ Grilling  what does travel show while…         with you   [Start]
 ▸ Orders                   │  ⛵ Prototype the progression graph                ship quiet-heron
   Review requested    2    │  ○ Grilling  where does the map region sit…       blocked by: ship figures 
   My PRs              5    │  ○ Grilling  pacing target for the milestone…     blocked by: map region, ship 
   Assigned to me      7    │  ✓ Task      split the ADR into implementation…   closed · gist ↗
   Tracker: Sprint 41       │  ▸ Decisions so far (6)    ▸ Not yet specified
```

- The sidebar gets a **Campaigns** group per fleet. Each row shows progress (`subIssuesSummary`) and a count of with-you children ready (●2), the only accent color.
- The campaign view is a tree in map order. Status glyphs: ✓ closed, ◉ ready, ○ blocked (naming its blockers), ⛵ ship underway (tap to board), and a "needs you" mark when that ship waits. Nested sub-trees fold, so a nested map shows inside its parent.
- Dragging a child onto a dock still works, since a child is an order.
- Ready with-you children are **not** inbox items, because the inbox stays "what ships wait on". The campaign badge and AdmiralBot's chat question surface them. This is an open question below.

### Cross-repo

A child's order carries its own `repo`, as orders do today. A campaign also keeps a **work checkout**, so tickets filed in a docs repo launch in the code repo by default. ADR 0009 preselects from campaign, then order. Sub-issues may come from other repositories, so the tree query fetches each node's `repository`.

### GitHub reads (primary sources)

- GraphQL `Issue.subIssues`, `subIssuesSummary`, `parent`, `blockedBy`, `blocking`, `assignees`, `repository`: <https://docs.github.com/en/graphql/reference/objects#issue>. I checked these live with `gh api graphql` against the owner's maps. One query of about 3 points reads a whole level.
- REST sub-issues (`GET …/issues/{n}/sub_issues`, `…/parent`, `PATCH …/sub_issues/priority`): <https://docs.github.com/en/rest/issues/sub-issues>
- REST dependencies (`GET/POST …/dependencies/blocked_by`, `GET …/dependencies/blocking`): <https://docs.github.com/en/rest/issues/issue-dependencies>
- Limits: at most 100 sub-issues per parent and 8 levels of nesting; sub-issues may come from another repository: <https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues>. The cross-repo map is at 91, which probably explains its `Part of` children. So the reader also parses `Part of #<n>` and `Blocked by: #…` body lines as the fallback, as the tracker doc describes.
- Search qualifiers for trees are missing from GitHub's filtering doc. In my tests `is:blocked` and `has:parent-issue` work under advanced search and `parent-issue:` doesn't. Discover campaigns by search (`label:wayfinder:map`, `has:sub-issues`, or one the engineer pins by URL), then read each tree with GraphQL.

### Plugins (a work tracker)

Add an optional `tree` capability to the plugin protocol:

- `campaigns.list` returns `[{id, title, url, progress}]`.
- `campaigns.tree {id}` returns order nodes with `parent`, `position`, `state` (`open`/`closed`), `blockedBy: [id]`, `assignee`, `labels` and an optional `mode` (`afk`/`with-you`).
- `campaigns.changed` is the matching notification.

Orders keep their current fields, so a plugin without `tree` is unchanged. A tracker's epics with subtasks map straight onto it. If the tracker has no blocking, the ready set is just the open, unassigned children.

## Open questions for the owner

1. Is **campaign** the right word, or would you rather see **chart** (closer to wayfinder) or plain "map"?
2. Do you follow a campaign by pinning one, or should every open `wayfinder:map` and spec with sub-issues in a fleet's repos appear on its own?
3. Should ready with-you tickets enter the inbox, or stay as a campaign badge plus AdmiralBot's chat questions?
4. When a with-you ship starts, should Admiral open its bridge right away (grilling is live conversation)?
5. Should AdmiralBot launch AFK children on its own (campaign autopilot), or always ask first?
6. Can one Commodore ship take a multi-child slice, or should it be strictly one ship per child?
7. Should Admiral ever write to the tree (claim by assigning, for example), or leave every write to the skill?
8. Does the work tracker have parent/child and blocking at all, and through what CLI or API?

## ADRs this would change

- **0006** (plugins): amend for the `tree` capability, and for GitHub becoming built in (that may be its own ADR in the redesign).
- **0009** (checkout): preselect the campaign's work checkout for cross-repo maps.
- **0020** (AdmiralBot through the CLI): add campaign reads, and AdmiralBot launching ships.
- **0017** and the GLOSSARY's **Autopilot**: autopilot attaches to a campaign as well as a panel, and launches only AFK children.
- **New ADR**: "Campaigns are read from the tracker's tree; Admiral never edits it."
- **GLOSSARY**: add **Campaign**, and **Ready** / **With you** for a child. "Sources belong to Admiral, not to a fleet" is already changing in the redesign.
