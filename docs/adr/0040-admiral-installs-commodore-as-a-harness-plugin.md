# 40. Admiral installs Commodore as a harness plugin

Status: accepted

## Context

Admiral needs Commodore: built-in crew members come from the installed Commodore skill's `crew/`, and Commodore is the default skill on every launch. Until now the engineer installed it by hand, and a fleet without it showed "Commodore not found". [#584](https://github.com/jbuffin/admiral/issues/584) decided how Admiral offers to install it while Commodore stays its own package that works without Admiral.

## Decision

**Admiral installs the `jbuffin/skills` package as the `jasonbuffin@jbuffinskills` plugin, through each harness's own plugin commands, and reads its version from the installed plugin's `.claude-plugin/plugin.json`.**

- **What installs.** The whole package, not Commodore alone: Commodore runs its sibling skills' scripts by relative path, and `crew` ships beside it.
- **How.** `claude plugin`, `codex plugin` and `copilot plugin`: add the `jbuffin/skills` marketplace, then install the plugin, with the fleet's environment so it lands in that fleet's identity at user level. All three harnesses read the package's `.claude-plugin/` manifest. Admiral never runs `npx skills add` and never writes skill files itself.
- **Detected** means a user-level copy or an enabled plugin. A copy in one repo's skills folder doesn't stop the offer.
- **Where it's offered.** In `admiral setup` for each fleet and harness without Commodore, and as **Install Commodore** where the launch dialog said "Commodore not found". Nothing is remembered on decline. The setup answers file's `install_commodore` defaults to `false`.
- **Versions.** Admiral carries a minimum Commodore version. Below it, Admiral warns and offers **Update**, and never refuses a launch. A newer version raises nothing. A loose `commodore` skill (from `npx skills` or copied in) has no version Admiral can read: Admiral says so and how to switch, offers no Update and deletes nothing.
- **Updates are the harness's job.** Codex updates marketplace plugins at startup. On Claude Code and Copilot, Admiral turns on auto-update for the marketplace it added. **Update** runs the harness's update command for the gap. Admiral never changes the update setting of a Commodore it didn't install.
- **Engineer only.** Only an engineer's click or setup answer installs or updates. AdmiralBot, routines and ships cannot ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)).

## Considered options

- **Each harness's plugin commands** (chosen). Versioned on every harness, updated by the harness, and Admiral owns no files in the identity.
- **`npx skills add`.** Needs Node, records only a folder hash, and Codex and Copilot would have no version to read.
- **A pinned copy shipped with Admiral.** Works offline with a known version, but Admiral would own files in skill folders and Commodore's updates would ride Admiral's releases.
- **An Agent Plugins `plugin.json` at the package root.** Open and versioned, but it only looks one level under `skills/`, so `jbuffin/skills` would have to restructure, and no harness needs it: all three already read `.claude-plugin/`.
- **`metadata.version` in Commodore's `SKILL.md`.** Readable however Commodore was installed, but a second copy of the version to keep in step, and unnecessary once every install is a plugin.

## Consequences

- `skillindex` reads Codex and Copilot plugins, not only Claude's, and a project can't stand in for a user's disabled Commodore plugin on any harness ([ADR 0010](0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md)).
- Plugin skills are namespaced, so the launch prompt is `$jasonbuffin:commodore` on Codex and `/jasonbuffin:commodore` on Claude Code and Copilot.
- Codex also reads `~/.agents/skills`, so a loose `commodore` there still wins over the plugin until the engineer removes it.
