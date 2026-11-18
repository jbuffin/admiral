# 46. Allow rules are Admiral's, added to ships at launch

Status: accepted

## Context

Near the end of a run, ships stop on permission prompts that need the engineer, even with each harness's assisted permissions on. Copilot does this most. [#803](https://github.com/jbuffin/admiral/issues/803) asked that a prompt the engineer allows by hand become a lasting rule, offered in the Inbox and kept only on the engineer's yes. Each harness already keeps lasting rules in its own config directory, in its own format: Claude Code's `permissions.allow`, Codex's `rules/*.rules`, and Copilot's `permissions-config.json`, which is saved per repo. Admiral launches every ship and already adds its hooks to each launch.

## Decision

**Admiral keeps the allow rules for each fleet in its own store, the way it keeps crew notes. It adds them to each ship as the ship launches, never to the harness's own config. Codex is the one exception.**

- **Claude Code:** each launch gets a settings file that carries the ship's allow rules for its repo. Claude Code merges an allow list from `--settings` with the engineer's own.
- **Copilot:** each rule becomes an `--allow-tool` flag on the launch, which lasts for that run.
- **Codex:** Codex has no launch flag for rules, so Admiral writes its rules into one file it owns, `rules/admiral.rules`, in the fleet's `CODEX_HOME`. Codex reads every `*.rules` file there. The engineer's own rule files are never touched. A Codex rule therefore covers every repo in the fleet, and its proposal says so.
- **Scope:** a rule belongs to one harness, one fleet and one repo, where the repo is its remote's `owner/repo`, or its path when it has no remote.
- **Undo:** the fleet's settings list every allow rule and every skipped proposal, and either can be removed there.

## Considered options

- **Admiral's own store, added at launch** (chosen). Admiral can list and undo every rule in one place, can scope a rule to one repo, and never edits a file the harness also writes.
- **Write each harness's own config.** The rules would also apply to sessions started outside Admiral. But Admiral would edit three formats that the harnesses lock and rewrite, a rule would be hard to find again to undo, and Claude Code's user settings can't scope a rule to one repo.

## Consequences

- Sessions the engineer starts outside Admiral don't get Admiral's allow rules.
- Copilot's assisted judge must honor `--allow-tool` rules. Confirm that with a probe on the Copilot version in use before relying on it.
