# 42. A ship's result comes back as a card and a report turn

Status: accepted, amended by [ADR 0043](0043-an-idea-in-the-chat-becomes-a-pr-with-no-questions-on-the-way.md) (Ready to merge and the end of a ship AdmiralBot launched each post a card) and [ADR 0045](0045-a-fleet-digest-is-a-routine-action-that-cannot-act.md) (the report-turn queue also carries fleet digest turns) and by a later change to its Report-turns bullet (an error exit, anchored, reports; only needing the engineer queues nothing)

## Context

[ADR 0041](0041-admiralbot-sends-scouts-quick-ships-and-commodore-ships.md) has AdmiralBot send scouts, quick ships and Commodore ships. [#658](https://github.com/jbuffin/admiral/issues/658) asked how their results reach the engineer through AdmiralBot, the way Grok Bot, ChatGPT and Claude Duet report background work back through the main agent. The research for the map ([#656](https://github.com/jbuffin/admiral/issues/656)) found one shape in every product that does this well: the coordinator gets the result as an event in a later turn, never guesses at unfinished work, checks "completed" against what was asked, and replies with a short message and a link to a real artifact.

Today nothing wakes AdmiralBot when a ship ends. [ADR 0024](0024-admiralbot-memory-is-a-server-owned-ledger.md) says the model is never woken just to narrate, and ship events reach it only as the digest before the engineer's next message, on Claude Code alone. The Admiral report holds a result, PRs and resume text, with no place for what a scout found, and it is removed at sail-off. AdmiralBot has no tool that reads a campaign's tree.

## Decision

**When a ship AdmiralBot launched from a chat turn finishes, the server posts a card at once and queues a report turn. AdmiralBot answers the report turn with a short summary and links. A ship can leave findings, a file Admiral keeps after sail-off.**

- **The card.** When any ship in the fleet sails off, ends with an error, is lost or first moors with an open PR, the server posts a card in the chat: the ship, its kind, its result and links to its findings, PRs and conversation. It needs no model turn, and it never pushes.
- **Report turns.** A ship launched via AdmiralBot on an engineer turn also queues a **report turn** for those same events. A ship launched from a commitment, a campaign or by hand gets the card only, so "work on #n" does not post a message per child. Needing the engineer (a question or a permission prompt) queues no report turn: it goes to the Inbox as before, and AdmiralBot never answers it. An error exit (anchored) does queue a report turn.
- **Scouts launched together report together.** Scouts launched from one chat message form a group, and the group's report turn comes when the last of them ends. Each scout's card still posts as it ends. Quick and Commodore ships always report on their own, so a scout's answer never waits on a build.
- **Delivery.** Report turns wait in a server-side queue per fleet and are typed into the chat when it is idle (no turn running, no chat question open), like a routine's `[Routine: …]` turn, so they work on every harness. They are never merged into the engineer's turn. A reset or New chat waits until the queue is empty, so no report is lost to a reset.
- **A report turn is not the engineer's turn.** The server's turn gate ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)) refuses every AdmiralBot write during it. AdmiralBot reads and talks; it offers follow-up work ("Want me to make that a PR?") and acts only on the engineer's answer.
- **What the turn carries.** The ship, its kind, the launch text, the report's result, its PRs, and whether it left findings. Not the findings themselves.
- **Completed is not succeeded.** AdmiralBot says "done" only when the result is `done` and the deliverable exists: findings for a scout, a PR for a quick or Commodore ship. Anything else it reports as partial or failed, with the reason and a link to the ship's conversation.
- **The reply.** A short summary plus links to the findings, the ship's conversation and any PR, threaded under the card. It never pastes the findings whole. What happens to a PR after this first report is [#659](https://github.com/jbuffin/admiral/issues/659)'s.
- **Findings.** Any ship may leave **findings**, a markdown file beside its Admiral report. A scout must: a scout that ends without findings has the result `failed`. Admiral keeps the findings with the ship's outcome after sail-off, and the card opens them. A new read, `read_findings(ship)`, returns them to AdmiralBot, cut at about 8,000 characters. AdmiralBot treats them as quoted data from a ship that had the web; the turn gate refuses writes in a report turn, and in an engineer turn only `read_findings`'s description and note, the chat prompt and the fleet fence guard against a planted instruction in them.
- **Pushes.** AdmiralBot's reply to a report turn pushes under [ADR 0033](0033-pushes-are-web-push-from-the-mac-with-no-hosted-relay.md)'s "not while watching" rule, and tapping it opens the chat at that message. It does not add to the attention count, since nothing waits on the engineer.
- **Campaigns.** A new read, `read_campaign(id)`, returns a campaign's tree: each child's state, blockers, whether it is ready, and its live ship. AdmiralBot uses it when asked what is left on a campaign, and in a report on a ship working a campaign child.

## Considered options

- **A card and a report turn** (chosen). The card makes the result visible without the model; the report turn gives the Grok Bot–style answer through AdmiralBot.
- **Cards only, and the digest on the next message** (today). Cheap, but nothing reports back, there's no summary, and the digest is Claude only.
- **A report turn only.** The result would then depend on a model turn going through.
- **Findings as the ship's last message, read with `read_conversation`.** No new file, but the result would live in a transcript that goes with the session, in a shape Admiral doesn't control.

## Consequences

- Amends [ADR 0024](0024-admiralbot-memory-is-a-server-owned-ledger.md): AdmiralBot is woken for a report turn on a ship it launched from a chat turn, and a reset waits for queued report turns.
- Amends [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md): `read_findings` and `read_campaign` join its reads, and a report turn is not an engineer turn.
- Amends [ADR 0033](0033-pushes-are-web-push-from-the-mac-with-no-hosted-relay.md): AdmiralBot's report pushes, outside the attention count.
- Each report is a model turn, so a fleet with many short ships pays for many short turns.
