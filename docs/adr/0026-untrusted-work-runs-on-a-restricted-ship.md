# 26. Work from untrusted text runs on a restricted ship that cannot push

Status: accepted

## Context

[ADR 0025](0025-outside-events-are-quarantined.md) keeps the Lookout away from tools, but the ship it leads to still has to read the event to act on it. A ship holds the repo, which is private data, so the event's text could steer it into sending that data out. The research in [#219](https://github.com/jbuffin/admiral/issues/219) ([note](../research/worker-guardrails.md)) proposed cutting the send-out leg instead of the data, and [#224](https://github.com/jbuffin/admiral/issues/224) decided how far.

## Decision

**A ship launched from untrusted text (A3) runs on a restricted profile that cannot send anything out, in any fleet. Pushing and PRs start only from a new normal ship the engineer promotes.**

- **The untrusted text is a file.** Go builds the ship's prompt from the routine's template, which names `.admiral/untrusted/event.md` as text written by others, to be treated as data. The raw event goes in that file, never on the command line.
- **Two levels.**
  - **Investigate**: read-only, ending in a report.
  - **Draft fix**: writes inside its worktree and commits locally only.
- **No way out.** On both levels the network is limited to package registries, there is no GitHub token, and web tools, MCP, `git push` and `gh` writes are denied. The sandbox fails closed. A restricted ship never pushes, in any fleet, so Home has no draft PR either.
- **Claude Code only, for now.** On Claude Code the profile is `--permission-mode dontAsk` with Edit and Write allowed in the worktree, `--setting-sources project`, a sandbox that fails if unavailable with no unsandboxed commands, `network.allowedDomains` limited to package registries, and `WebFetch`, `WebSearch`, `mcp__*`, `Bash(git push *)` and `gh` writes denied. Never `auto` or `bypassPermissions`. Codex comes once its `workspace-write` diff-only ending has been tried, a build task. Copilot stays out until a sandbox flag is confirmed.
- **Autopilot mode too.** A restricted ship is an unattended launch, so it runs in autopilot mode (ADR 0034) with the restricted profile on top: the guard and the advisor run, and every restriction above still holds. Decided by the engineer while this was recorded (2026-11-10).
- **Without a capable harness.** Where no harness in the fleet can run a restricted ship, A3 drops to A1 (propose) and never rises to A4. A Draft fix routine shows as reduced to Propose ([#241](https://github.com/jbuffin/admiral/issues/241)).
- **Promote** starts a new normal ship from the trigger-log record, and counts as a T0 act. It takes the restricted ship's local commits or diff as its starting point and never continues the tainted session. Pushing and PRs start here. The reviewer doesn't run on it.

## Considered options

- **No push in any fleet** (chosen). A push is a way to send data out, so allowing one only at Home would leave the leg open where it is easiest to forget.
- **A draft PR at Home from an `admiral/*` branch.** Convenient, but it needs a push rule in the guard and reopens the way out.
- **Promote continues the same session.** Faster, but its context already holds the untrusted text.

## Consequences

- Amends [ADR 0013](0013-each-harness-starts-with-the-prompt-on-its-command-line.md): a worker-launched ship's prompt is built by Go from a template, and the outside text it acts on goes in a file in the checkout, not on its command line.
- A Draft fix ends in local commits. Someone has to Promote it before anything reaches GitHub.
- A fleet whose only harnesses are Codex or Copilot gets proposals, not restricted ships, until those harnesses are proven.
