# 22. A UI save of autopilot args passes the crew allowlist

Status: accepted

## Context

A panel's autopilot entry (`sources.<key>.autopilot.<panel>`) has `args`, extra harness arguments that every autopilot ship from that panel starts with, placed after the dock's own. The parser checks only that they are strings, and the launcher passes them on as they are. Until now only a hand edit or `admiral config edit` could set them, so whoever set them could already edit the fleets' executables and environment.

[#181](https://github.com/jbuffin/admiral/issues/181) adds an Autopilot form to every panel, so the entry can be set from an iPad. ADR 0010 rejected a settings screen partly because it "would turn a LAN browser into an editor for executables and environment". An editable `args` field brings part of that back: from a LAN browser, one save could add `--dangerously-skip-permissions` or `--settings` to every ship a panel launches unattended. Admiral already has rules for which arguments an unattended agent may add: `launcher.AutopilotArgs`, the allowlist that `admiral crew launch --arg` meets when `ADMIRAL_AUTOPILOT=1` is set (#175).

## Decision

**A save from the Autopilot form must pass `launcher.AutopilotArgs` for the `args` it writes, checked against the args of the chosen fleet's claude dock. A hand edit is not checked.**

- **The same rules as crew.** For Claude Code, the allowed flags are `--model`, `--advisor`, `--effort`, `--disallowedTools` (not when the dock already sets it) and `--permission-mode` no looser than the dock's. A refused save returns the allowlist's message (`--arg <value> is refused: …`), the form shows it, and the file is left unchanged.
- **Hand-set args that fail are kept, not edited.** When the stored `args` fail the allowlist, the form shows them read-only and says why. A save keeps them byte for byte, and the check is skipped for that save. The engineer changes them in `config.json`.
- **Hand edits stay unchecked.** Whoever edits `config.json` can already change the fleets' executables and environment, so checking `args` there would protect nothing. Doing it would also turn a working config into an invalid one on upgrade.
- **Only one harness.** The form writes `"harness": "claude"` because autopilot runs only on Claude Code. When a harness is added to `AutopilotHarnesses`, its allowlist covers UI saves for it too.

## Considered options

- **Check a UI save against the crew allowlist** (chosen). It reuses rules that already exist and were checked against the harness's parser, and it keeps the form useful from an iPad.
- **Editable args with no check.** The form would become an editor for every argument an unattended ship runs with, from any browser holding the token. That is the reach ADR 0010 kept out of the UI.
- **Args read-only in the form.** Safe, but choosing a model or effort for a panel is most of what the field is for, and it would still need a hand edit.
- **Check hand edits too, in the parser.** Hand edits already have more reach than the args give, and an existing config that sets other flags would stop loading.

## Consequences

- A panel's args can hold flags the form can't write. They come from a hand edit, and the form shows them read-only.
- A change to the crew allowlist changes what the form accepts. A stored entry that passed before may then show read-only.
- The known gaps of the crew allowlist apply here too. For example, a dock with no `--permission-mode` whose `CLAUDE_CONFIG_DIR` settings set a stricter default mode is compared against `default`.
