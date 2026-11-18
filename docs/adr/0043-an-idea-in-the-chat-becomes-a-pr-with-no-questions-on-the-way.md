# 43. An idea in the chat becomes a PR with no questions on the way

Status: accepted

## Context

The map [AdmiralBot as a personal assistant](https://github.com/jbuffin/admiral/issues/653) wants the engineer to say "add X to Admiral" and come back later to a PR. [ADR 0041](0041-admiralbot-sends-scouts-quick-ships-and-commodore-ships.md) has AdmiralBot choose a scout, a quick ship or a Commodore ship, and [ADR 0042](0042-a-ships-result-comes-back-as-a-card-and-a-report-turn.md) brings the result back. [#659](https://github.com/jbuffin/admiral/issues/659) asked what happens between the two: how the request becomes a launch, where the ship works, and where anyone may still have to ask.

The research for the map ([#654](https://github.com/jbuffin/admiral/issues/654)) found three gaps in the launch tool. Only Commodore launches run in autopilot, though [ADR 0034](0034-autopilot-is-a-launch-mode-not-a-view-setting.md) covers every ship. At the global cap an autopilot launch quietly relaunches as interactive. And `launch_ship` drops `resources` and can't set a model or effort. A quick ship also has no skill to make a worktree, so under [ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md) it would edit the engineer's own checkout.

## Decision

**AdmiralBot restates the request as the ship's launch text and launches in autopilot, even over the cap. A quick ship works in a worktree Admiral makes for it. After launch, nobody asks AdmiralBot anything: the ship's questions go to the Inbox, and the PR's later milestones come back as cards.**

- **Autopilot for every AdmiralBot launch.** Every `launch_ship` runs in autopilot mode unless the engineer says "with me": a scout, a quick ship, a Commodore ship, or any other launch. [ADR 0034](0034-autopilot-is-a-launch-mode-not-a-view-setting.md) stands; the code drops its Commodore-only test.
- **Never blocked by the cap, never switched.** A launch on the engineer's current instruction counts toward `max_ships` but is never blocked by it, and it keeps its mode. The relaunch as interactive at the cap goes. The cap still holds back every unattended launch.
- **Launch settings.** `launch_ship` takes `resources`, `model` and `effort`. AdmiralBot sets them only when the engineer names one, or when the fleet's map calls for a resource; otherwise the dock's defaults apply. A named resource with no free unit refuses the launch, and AdmiralBot says so; an instruction launch doesn't wait.
- **The launch text is AdmiralBot's restatement.** AdmiralBot writes what the ship is told: the goal, the repo, what done looks like (an open PR, or findings for a scout) and pointers from the chat such as links and earlier findings. It keeps exact strings verbatim (error text, names, paths, URLs, issue numbers) but not the engineer's whole message, which may ramble. The launch line in the chat shows the kind and the reason, and the full launch text behind a disclosure, so a bad restatement can be caught.
- **The closing steps are Admiral's.** On every chat launch that should end in a PR, Admiral appends fixed closing steps to the launch text: commit on the branch, push, open a PR, and write the Admiral report with `result` and `prs`. AdmiralBot does not write them, so no restatement can drop them, and without the report the ship could not moor ([ADR 0028](0028-a-ship-with-an-open-pr-moors-and-relaunches-for-each-feedback-round.md)) or report done ([ADR 0042](0042-a-ships-result-comes-back-as-a-card-and-a-report-turn.md)).
- **A quick ship runs no skill.** It launches with skill `none` and the closing steps. A per-fleet quick-ship skill can come later if one is wanted.
- **Which checkout.** AdmiralBot picks the repo from the fleet's map and asks only when more than one repo fits. A scout reads the fleet's existing checkout, and its guard refuses edits. A Commodore ship launches in the existing checkout and makes its own worktree, as it does today. A clone happens only when the fleet has no checkout of the repo ([ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md)'s amendment).
- **Admiral makes a no-skill ship's worktree.** For a ship launched with no skill, Admiral fetches and adds a git worktree on a new branch from the remote's default branch, not the checkout's HEAD, and the ship runs there. Nothing nests, since no skill makes its own. The worktree lives as long as the ship: a moored ship keeps it, because each feedback round relaunches in the same checkout. Return to port removes it when the ship ends ([ADR 0036](0036-a-ship-returns-to-port-to-clean-up-before-it-ends.md)) and keeps the branch, which the PR points at.
- **Where it may still ask.** Only before a launch: when the request is unclear ([ADR 0041](0041-admiralbot-sends-scouts-quick-ships-and-commodore-ships.md)), or when more than one repo fits. After launch, a ship's high-stakes calls, harness prompts and anchoring go to the Inbox as before, and AdmiralBot neither relays nor answers them.
- **After the first PR report.** The ship moors and follows reviews ([ADR 0028](0028-a-ship-with-an-open-pr-moors-and-relaunches-for-each-feedback-round.md)). For a ship AdmiralBot launched, the chat also gets a card, with no model turn, when the ship becomes **Ready to merge**, and another when it returns to port or sails off. Feedback rounds get no report turn: their findings item is already in the Inbox.

## Considered options

- **Autopilot over the cap** (chosen), against **switching to interactive** (today), which leaves the engineer to come back to a ship waiting on them, and **waiting for room**, which turns an instruction into a queued launch. An interactive ship over the cap costs the machine as much as an autopilot one.
- **The engineer's words verbatim, then framing**, against **a restatement** (chosen). Chat messages ramble, and the disclosure lets the engineer check the restatement.
- **An Admiral worktree for a no-skill ship** (chosen), against **the launch text asking the ship to make one**, which is a prompt rule on an unattended ship, and Codex and Copilot have no built-in worktree tool. **A fresh clone per ship** is a whole checkout for a small change.
- **Cards for Ready to merge and the end** (chosen), against **nothing after the first report**, which leaves the chat silent about the outcome the engineer asked for, and **a report turn at each milestone**, which pays for a model turn that adds nothing to a card.

## Consequences

- Amends [ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md): Admiral never creates its own worktrees, except for a ship launched with no skill.
- Amends [ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md): every AdmiralBot launch runs in autopilot unless "with me", not only Commodore ones; the launch tool takes `resources`, `model` and `effort`; and an instruction launch keeps its mode over the cap.
- Amends [ADR 0027](0027-campaigns-are-read-from-githubs-issue-tree.md): the autopilot default covers every ship AdmiralBot launches, not only Commodore ships.
- Amends [ADR 0042](0042-a-ships-result-comes-back-as-a-card-and-a-report-turn.md): Ready to merge and the end of a ship AdmiralBot launched each post a card.
- A quick ship no longer edits the engineer's checkout, and its worktree is one more thing Return to port removes.
- Amended 2026-11-17: a drafting launch always runs With you, as if the engineer said "with me", because its skill grills the engineer. The rest holds: the launch text is AdmiralBot's restatement, and the cap never blocks it ([#705](https://github.com/jbuffin/admiral/issues/705)).
