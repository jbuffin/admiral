# 41. AdmiralBot sends scouts, quick ships and Commodore ships

Status: accepted

## Context

The map [AdmiralBot as a personal assistant](https://github.com/jbuffin/admiral/issues/653) wants the engineer to give AdmiralBot an idea in the chat and get the work back, as Grok Bot, ChatGPT and Claude Duet do. [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md) keeps AdmiralBot from doing any work itself. [#657](https://github.com/jbuffin/admiral/issues/657) asked whether that fence loosens, and what kinds of ship AdmiralBot sends instead. The research for the map found that the strong assistants coordinate from one conversation and hand the work to child runs.

## Decision

**The fence stays. AdmiralBot knows Admiral's map of its fleet, and it gets work done by choosing a kind of ship: a scout, a quick ship or a Commodore ship. The engineer never names a kind.**

- **The fence stays.** AdmiralBot never reads a repo's files, logs or the web, and never runs a shell. Looking inside anything is a scout's job.
- **It reads the fleet's map.** New Admiral MCP reads list the fleet's repos and checkouts (from its search roots and clone root, and which repos can be cloned), its docks and each harness's probed capabilities, its crew members and its skills. That way a launch lands in the right checkout without a guess. These are reads of Admiral, so the fence and its probes don't change.
- **AdmiralBot chooses the kind.** The engineer says what they want; AdmiralBot picks how to set it up and states the kind and the reason in the line that reports the launch ("Sent a scout to read the AWS logs"). The engineer corrects it in plain words ("make that a PR"). It asks a chat question only when the request itself is unclear, never to choose a kind.
- **Scout.** A ship that looks at something and reports back. Its guard runs in scout mode and refuses `git commit`, `git push`, opening or changing a PR, and file edits in its checkout. It keeps its web, MCP tools and GitHub login, so it is not a restricted ship. It runs to the end, writes its report and sails off on its own. It never anchors to wait for the engineer, and anything it can't settle goes in the report as an open question. A harness without a proven guard can't run a scout.
- **One scout or several.** Questions about one thing (a repo, an incident, a topic) go to one scout, which may use its harness's own subagents to read in parallel. Unrelated questions, or ones in different repos, each get their own scout, launched together. AdmiralBot puts their reports together in the chat.
- **Quick ship or Commodore ship.** A quick ship when one session can finish the change and there is no design to settle. A Commodore ship when there is a design to settle, several slices or a team. When AdmiralBot can't tell, it sends a Commodore ship.
- **The kinds are defaults, not a menu.** When a request fits none of them, AdmiralBot can launch with anything `launch_ship` takes (a skill, crew members, issues, a harness, "with me") and says what it sent and why. Such a launch is an ordinary unattended ship under [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md) and [ADR 0034](0034-autopilot-is-a-launch-mode-not-a-view-setting.md), not a scout, and gives AdmiralBot no new power.

## Considered options

- **Keep the fence; add the fleet's map and named kinds** (chosen).
- **Loosen the fence to read repos or the web.** The work would fill AdmiralBot's long-lived context, and Codex and Copilot can't be held to "reads only", so every fence proof would have to be redone.
- **One undifferentiated launch, with the brief carrying the intent.** A scout's "changes nothing" would then rest on its prompt alone, on a ship that runs unattended.
- **Several researchers as one scout's crew boats.** Crew boats stay for building under one lead. For research, AdmiralBot is already the coordinator that puts reports together.

## Consequences

- Amends [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md): the fleet-map reads join its reads, and the scout guard mode joins the autopilot guard.
- How a scout's report and any ship's result reach the engineer through AdmiralBot is decided by [#658](https://github.com/jbuffin/admiral/issues/658). What happens after a quick or Commodore ship opens its PR is decided by [#659](https://github.com/jbuffin/admiral/issues/659).
