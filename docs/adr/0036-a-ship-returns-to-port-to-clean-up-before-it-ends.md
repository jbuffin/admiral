# 36. A ship returns to port to clean up before it ends

Status: accepted

## Context

Stop ship kills a session mid-thought, and a moored ship whose PRs merge sails off with no session at all. Either way, whatever the run set up for itself (a worktree, a branch, a background process, a scratch file) stays behind. Admiral can't know what that is: it depends on the skill and the repo.

## Decision

**Admiral tells a ship to clean up after itself, never how, and stops it when that turn ends.**

- **Return to port** is the bridge's button for a live ship that is done. Once the ship's own turn ends (needs you, reason `stop`), Admiral types one fixed clean-up prompt. When the turn that prompt starts ends the same way, Admiral stops the ship as Stop ship does. A turn that stops on a question or an approval waits for the engineer.
- **A ship that carries a PR** doesn't offer the button: its work isn't done until review is. When the moored-ship poll sees every PR it carries done, at least one of them merged (a stack once every layer is), Admiral wakes it with its resume text and the clean-up prompt, and stops it when that turn ends. A ship that can't be woken (no valid resume text, or a launch that fails) sails off as before. A ship whose PRs were all closed unmerged sails off as before.
- The progress lives in the session option `@admiral-returning` (`queued`, then `sent`), so it survives an Admiral restart.

## Consequences

- A merged ship takes one more turn and counts toward the cap as a hand wake does, without being blocked by it.
- Amends [ADR 0028](0028-a-ship-with-an-open-pr-moors-and-relaunches-for-each-feedback-round.md): a merge now wakes the ship for a clean-up turn before it ends.
