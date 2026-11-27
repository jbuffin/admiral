# 50. The engineer picks the default fleet

Status: accepted

## Context

The default fleet is the first fleet in the configuration ([ADR 0006](0006-order-sources-are-subprocess-plugins.md)). It takes the GitHub items no project or owner matches and the plugin sources a fleet didn't name ([ADR 0010](0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md)). Which fleet comes first depends on the order setup wrote them, not on anything the engineer chose.

[#937](https://github.com/jbuffin/admiral/issues/937) settled how a client gives the engineer one point of contact across fleets: a fleet switcher, one Inbox, no AdmiralBot above the fleets'. A voice command or a call that names no fleet ("ask AdmiralBot what's waiting") needs a fleet to go to, and the first fleet in the file is not a reliable answer.

## Decision

**The default fleet is the fleet the engineer picks, kept as a top-level `default_fleet` key in `config.json` that names a fleet key. Until they pick one, it is the first fleet in the configuration. It is one value for everything a default fleet already decides and for an AdmiralBot request that names no fleet.**

- **Where it's set.** On Preferences, and through the HTTP API, which serves the current default fleet to clients and lets a paired device change it. The CLI and `admiral setup` set it too. Every write keeps the other top-level keys, as ADR 0010 requires.
- **One value, not one per device.** A client shows and changes Admiral's value; it keeps no default of its own, so the same words go to the same fleet from any device.
- **A fleet that's gone.** When the key is missing or names a fleet that's not in the configuration (removed or renamed), the first fleet is the default and Preferences says so. Removing the fleet is never refused for it.
- **GitHub routing follows it.** The default-fleet step in ADR 0006's routing uses the picked fleet.

## Considered options

- **A top-level key that names a fleet** (chosen). Exactly one default by construction, and Preferences writes it without editing a fleet.
- **`"default": true` on a fleet.** It reads next to the fleet's other fields, but zero or two `true`s become states Admiral has to reject, and every change edits two fleets.
- **A separate default for voice and for GitHub routing.** It would let the two differ, but it adds a second term and a second setting. Whether GitHub should have a catch-all fleet at all, or route by the fleet the engineer is working in, is left for its own effort.
- **A default kept on each device.** "Ask AdmiralBot" would go to different fleets from different devices.

## Consequences

- Amends ADR 0006 and ADR 0010: the default fleet is the picked fleet, not always the first in the file.
- Reordering fleets in the file no longer changes the default once one is picked.
