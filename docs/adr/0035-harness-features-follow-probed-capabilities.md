# 35. Harness features follow probed capabilities

Status: accepted

## Context

Admiral runs ships on Claude Code, Codex CLI and Copilot CLI, but its extras were built on Claude Code. AdmiralBot's fence (ADR 0023), the judge calls (ADR 0025), restricted ships (ADR 0026) and autopilot mode ([ADR 0034](0034-autopilot-is-a-launch-mode-not-a-view-setting.md)) each need something from the harness that the docs claim but that a given version, login or config may not deliver. Autopilot mode today runs only on Claude Code (`launcher.AutopilotHarnesses`). [#241](https://github.com/jbuffin/admiral/issues/241) decided what an engineer with only Codex or only Copilot gets, and how Admiral knows a harness can carry a feature.

## Decision

**An engineer without Claude gets all of Admiral's core. Each extra turns on per harness only after the capabilities it needs are proven on this machine.**

- **The core works everywhere.** Ships, the bridge, the Inbox, With-you campaign work and moored PR rounds work on every harness. A Codex-only or Copilot-only setup is supported, reduced. Nothing is quietly Claude-only.
- **Capabilities, per feature:**
  - **Judge calls:** no tools, no network, JSON schema, per-call cap, usage reports.
  - **AdmiralBot:** an MCP-only fence, with the login kept in a separate config home.
  - **Restricted ship:** a sandbox that fails closed, with network limited to an allowlist.
  - **Autopilot mode:** a pre-tool guard hook, an args allowlist (`autopilotRules`) and the advisor.
- **A built-in table records what each harness version claims.** A capability counts only after its **probe** passes on this machine, for each fleet and harness version. A failed probe means the capability is missing, whatever the table claims.
- **Probes** run in `admiral setup` and again when the harness version changes. Results are stored in `$ADMIRAL_HOME`. A probe that needs a model call uses the cheapest model and a tiny prompt.
- **Features degrade one at a time.** A missing capability turns off only the feature that needs it, by the rules ADR 0025 records for judge calls.
- **AdmiralBot.** Its harness is a per-fleet setting in Preferences that lists only harnesses with a proven fence, and defaults to Claude when the fleet has it. With none, the fleet has no AdmiralBot: the chat shows as off with the reason, and so do commitments, campaign Without-you launches and routines whose Do prompts AdmiralBot. Routines that launch a ship, and hand launches, keep working.
- **Judge calls** (the Lookout, the reviewer, the filing drafter and the advisor). Admiral picks the harness: the first in the fleet with the required capabilities (no tools and no network), in the order Claude, Codex, Copilot. Each harness has a fixed model pair: a small model for the Lookout and a stronger one for the reviewer, the drafter and the advisor. The Routines and standing orders screen shows the harness and models, with no override.
- **Restricted ships.** Where no harness in the fleet can run one, A3 drops to A1 (propose) and never rises to A4. A Draft fix routine shows as reduced to Propose.
- **Autopilot mode on Codex and Copilot.** Each gets it once its guard probe passes. Both have `PreToolUse` hooks, and Codex hook trust is handled in the pane as [#239](https://github.com/jbuffin/admiral/issues/239) decided. Copilot's `--add-dir` and `--available-tools` must be settled in its args allowlist first. A launch that needs autopilot mode on a harness that lost it becomes an Inbox item and never runs unguarded (ADR 0034).
- **Showing what's off.** On Preferences and the Routines and standing orders screen, a feature that's off stays visible but greyed out. One line names the missing capability and the harness, and **Check again** re-runs that probe. Preferences has a per-fleet **Harness capabilities** list showing each capability as passed, failed or not checked.

## Considered options

- **Probe each capability on this machine** (chosen). A feature turns on only where its fence or guard has been seen to work, at the cost of a probe per fleet and harness version.
- **Trust a static table of what each harness supports.** Nothing to run, but a harness version or setup that doesn't do what the table claims would turn on a feature whose fence isn't there.
- **Claude only for the extras.** Simpler, but an engineer without Claude would get the core and nothing more.

## Consequences

- It is surprising that Admiral doesn't trust its own table. A capability the table lists still shows as not checked until its probe passes.
- Amends [ADR 0022](0022-a-ui-save-of-autopilot-args-passes-the-crew-allowlist.md): `AutopilotHarnesses` becomes the harnesses whose guard probe passed.
- Amends ADR 0023: the AdmiralBot harness is a per-fleet choice among harnesses whose fence is proven.
- Amends ADR 0025: A3 drops to A1 without a restricted-capable harness, and Admiral picks the judge harness and models. The advisor joins the judge calls.
- Amends ADR 0034: the advisor is a judge call on the fleet's judge harness.
- Trying Codex's `workspace-write` diff-only profile for restricted ships is a build task.
