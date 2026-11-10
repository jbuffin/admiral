# 6. GitHub is built in; other trackers are subprocess plugins

Status: accepted, rewritten 2026-11-10 for [#227](https://github.com/jbuffin/admiral/issues/227) (GitHub built in, sources per fleet, event methods, no freeform)

## Context

Orders come from many outside systems: GitHub through `gh`, an internal work system through its own signed-in CLI, and mail and calendars through Office 365 and Microsoft Graph. Each source needs its own way of fetching and its own auth. Other people should be able to add sources without a new Admiral release, and a broken source must not take the server down.

This ADR first made every source a subprocess plugin, with a freeform list and a `github` plugin that Admiral embedded and installed itself. The redesign leans on GitHub far beyond a list of orders: views, campaign trees ([ADR 0027](0027-campaigns-are-read-from-githubs-issue-tree.md)), PR watching ([ADR 0028](0028-a-ship-with-an-open-pr-moors-and-relaunches-for-each-feedback-round.md)) and events that start routines ([#226](https://github.com/jbuffin/admiral/issues/226)), and every fleet keeps its own sources ([#209](https://github.com/jbuffin/admiral/issues/209)). [#227](https://github.com/jbuffin/admiral/issues/227) decided the shape below.

## Decision

**GitHub is built into Admiral. Other trackers are long-lived subprocess plugins speaking JSON-RPC over stdio. Every source belongs to one fleet, and every GitHub item goes to exactly one fleet.**

- **One fleet per source.** No source is shared across fleets. The same GitHub account may back several fleets.
- **GitHub is core Go.** One client per fleet serves views, campaign trees, PR watching and events. It borrows `gh`: `gh auth token` for the fleet's host and optional `account` (by default the active account), so one account for everything works with no setup. The Python `github` plugin is retired. A GitHub App with Admiral's own OAuth is a later effort.
- **Plugins.** Any other tracker is a plugin Admiral starts and talks to over stdio, as LSP and MCP servers do, written in any language. It starts with the fleet's environment and owns its auth: it reuses an existing CLI login where there is one, and otherwise asks Admiral to show a sign-in URL or device code. It can push "orders changed" instead of waiting to be polled. An error answer to `orders.list` shows on its view, which keeps its last orders until the next list succeeds. The methods and messages are in [Order source plugin interface](https://github.com/jbuffin/admiral/issues/21).
- **Views.** A view is a named list of orders from one source. Built-in GitHub has Assigned to me, Review requested and My PRs by default, plus custom searches. Plugins keep `panels.list` on the wire, and Admiral shows each panel as a view.
- **Routing.** Each GitHub item goes to one fleet, first match wins:
  1. **Project:** its repo is checked out under the fleet's search roots.
  2. **Owner:** the repo's owner (an org or a user account) is listed in the fleet's `owners`.
  3. **Default fleet:** the first fleet in the config file.

  This applies to every built-in view and event, so nothing is hidden and nothing appears twice. A checkout beats an owner. When two fleets claim the same repo or owner, the first in the file wins and both show a warning.
- **Events.** GitHub's catalog: review requested of me, issue or PR assigned to me, @-mention of me, new item in view X. Plugins gain `events.catalog` and an `event` notification `{kind, ref, actor, author, verified}`. Go sets the tier from actor and author, the lower winning: the engineer's own login T1, allowlisted T2, others T3. A source never claims a tier. GitHub's `verified` is true, since the API authenticates the actor.
- **No freeform.** The freeform list is dropped. A typed prompt on a ship, or a request to AdmiralBot, does its job. No source is added on a fresh install, and the protocol docs keep an unshipped example plugin. AdmiralBot's "add an order" becomes a launch or a launch commitment, or `orders.add` on a plugin source that supports it.

## Considered options

- **GitHub built in, other trackers as plugins** (chosen).
- **GitHub as a plugin too** (this ADR's earlier shape). Retired: one client per fleet now serves views, campaign trees, PR watching and events.
- **Every source compiled into Admiral.** The simplest, but only an Admiral release can add a source.
- **A command that prints orders as JSON.** Trivial to write, but it can only be polled, and it can't run a sign-in or describe its settings.
- **WASM (Extism).** Sandboxed, but that sandbox makes it hard to shell out to a CLI or reach the network, which is what these sources do.
- **Existing MCP servers as sources.** They expose general tools, not "list my orders", so each would still need a mapping layer.

## Consequences

- A plugin crash stays in its own process and does not stop the server.
- Admiral needs good documentation for building a plugin, since others will write them. The example plugin in the protocol docs stays unshipped.
- `python3` is no longer an install dependency.
- Plugins run with the engineer's permissions; Admiral runs only the plugins the engineer installs.
- Today's global sources and freeform orders move into fleets by a startup migration ([ADR 0010](0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md)).
