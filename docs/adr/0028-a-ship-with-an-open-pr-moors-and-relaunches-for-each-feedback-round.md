# 28. A ship with an open PR moors on a clean exit and relaunches for each feedback round

Status: accepted

## Context

The ship that opened a PR stays with its change through review, and brings on the crew each round needs ([#210](https://github.com/jbuffin/admiral/issues/210)). Under [ADR 0008](0008-a-ship-is-anchored-when-its-harness-exits.md) a clean exit sails the ship off, so nothing is left to wake when a review lands, and the PR is the engineer's to carry. Answering reviews is this ship's own job, not a routine ([#226](https://github.com/jbuffin/admiral/issues/226)). Reviews can take days, and [#230](https://github.com/jbuffin/admiral/issues/230) decided how the ship holds across them.

## Decision

**A ship with an open PR moors when it exits cleanly. Admiral watches the PR and relaunches the ship under the same name for each feedback round. It never pushes a code change without the engineer's approval, and it sails off when every PR it carries is merged or closed.**

- **The same ship is the same identity, not the same process.** The harness exits once the PR is open. Each round is a fresh session under the ship's name, and the skill's on-disk state (Commodore's run directory and `state.md`) carries its memory.
- **What moors a ship.** Exit 0, and its report lists at least one PR that GitHub confirms is open and routes to the ship's fleet. Exit 0 without an open PR still sails off. A non-zero exit still anchors, and Dismiss ends following. Any ship that passes this test moors, whether launched by hand, by AdmiralBot or Promoted. Restricted ships never push, so they never qualify. Each ship has a **Don't follow reviews** switch. A PR opened by hand with no ship behind it is out of this: the engineer launches Commodore on it from the My PRs view.
- **The record.** `$ADMIRAL_HOME/moored/<ship>.json` holds the name, fleet, harness, model, skill, checkout, PRs, the report's `resume` text and the round count. It survives reboots, and the ship's name stays reserved while it exists. The file is the record only while the ship isn't live; on a wake Admiral recreates the tmux session under the same name, and tmux is the record again.
- **What wakes it.** Admiral polls the fleet's built-in GitHub client with one batched GraphQL query over every moored ship's PRs, every 2 minutes and when the Ships page opens.
  - Feedback: a review with comments or changes requested, new review-thread comments, PR comments.
  - Keeping it mergeable, on every layer of a stack: a failing check, a conflict with the base or the layer below, and *behind base* when branch protection requires up-to-date branches. Behind base without that rule doesn't wake it.
  - An approval on its own doesn't wake it.
  - A round starts 10 minutes after the last event, so one review with many comments is one round. One round runs at a time, and events during a round queue for the next.
- **Whose feedback wakes it.** The engineer's own comments (T1), allowlisted people and review bots (T2), including anyone requested as a reviewer on the PR, and failing checks from the repo's own CI. A stranger's (T3) comment never wakes it: it becomes an Inbox proposal, `Wake <ship> for @x's review?`, with launch or dismiss in Peek. The feedback text reaches the ship as data in `.admiral/untrusted/feedback-<N>.md`, never on the command line. A wake continues the engineer's own launch, so the ship is a normal ship, not a restricted one.
- **A proposal does not expire.** ADR 0025's 24-hour expiry is for its own proposals; reviews can sit for days, so this one stays until the engineer answers it or the mooring ends.
- **A feedback round.** The same harness, model, skill and checkout. The prompt is the report's `resume` text (Commodore's `resume <run-name>`), then `feedback round N on <PR>`. It is an unattended launch: it waits for room under the global cap and for its resources, and runs in autopilot mode ([#240](https://github.com/jbuffin/admiral/issues/240)).
  - The ship triages the feedback (fix, push back, needs your call) and posts one findings item to the Inbox. It fixes, pushes and replies only after the engineer approves the cards in Peek. This is Commodore's autopilot `pr-feedback` flow.
  - A clean rebase (`gh stack rebase` or a plain rebase with no conflicts) pushes without asking, and the round's report says so. Conflict fixes and CI fixes change code, so they go through the findings item. If a push dismisses existing approvals, the ship goes back to waiting on review.
  - Crew boats don't survive mooring, so each round brings fresh crew from the fleet's named crew. The skill picks them, and the engineer asks for anyone else in words.
  - The skill writes a fresh Admiral report at the end of every round, overwriting the last ([skills#195](https://github.com/jbuffin/skills/issues/195)).
- **Wake by hand.** **Wake** on a moored ship (Ships list, Peek, bridge), or typing at its bridge prompt, starts a round with the engineer's text after the `resume` text. It is a T0 act that works like a hand launch: it counts toward the cap but is never blocked by it. AdmiralBot can wake a ship on the engineer's instruction as one of its launch writes.
- **Merging.** Once the PR is approved, its checks are green and it is mergeable, the moored ship shows **Ready to merge** and a live Inbox entry appears, with no file, like a needs-you stop. Peek offers **Merge**, which uses the fleet's `gh` and the repo's default merge method and counts as the engineer's T0 act. A stack is ready only when every layer is; Merge runs `gh stack merge <top PR> --yes`, GitHub's atomic all-or-nothing merge. If only the lower layers are ready, Peek also offers **Merge up to #n**, and the ship stays moored on the rest. Nothing merges on its own.
- **Where PR state shows.** The built-in **My PRs** view puts state chips on each row (review, checks, mergeable, conflicts), the ship carrying the PR with its next step, and Merge. The Ships list has a **Moored** group with the same chips and how long each ship has waited. There is no separate PR dashboard.
- **End.** The ship sails off when every PR it carries is merged or closed. **Don't follow reviews** or **Dismiss** on a moored ship ends it too. There is no idle timeout, and a merge doesn't wake it for a wrap-up turn.

## Considered options

- **Moor, then relaunch under the same name** (chosen). Survives a reboot, costs no memory while waiting, and each round starts with a small context.
- **Resume the harness conversation** (`claude --resume` and the like). It needs conversation ids, differs per harness, and brings back a bloated context.
- **Keep the harness alive for days.** A reboot loses it, it costs memory per PR, and its context keeps growing.
- **GitHub webhooks.** Out of scope for now; polling serves, and webhooks are a later effort.

## Consequences

- Amends [ADR 0008](0008-a-ship-is-anchored-when-its-harness-exits.md): a clean exit with an open PR moors instead of sailing off, and a merged or closed PR now ends a ship.
- Amends [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md): the moored-ship file in `$ADMIRAL_HOME`, which reserves the ship's name while no tmux session exists.
- Amends [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md): outcome events for moored, woken, round and merged.
- Amends [ADR 0016](0016-answers-to-inbox-items-are-files-beside-the-item.md): Ready to merge is a second live Inbox entry with no file.
- The report schema gains a `prs` list, and its `resume` text becomes the wake prompt.
- Commodore takes `feedback round N on <PR>` and the feedback file into its `pr-feedback` mission when resumed, and pushes a clean rebase without a findings item. That change is in jbuffin/skills.
