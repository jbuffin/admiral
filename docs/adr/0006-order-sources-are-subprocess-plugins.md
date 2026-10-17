# 6. Order sources are subprocess plugins speaking JSON-RPC

Status: accepted

## Context

Orders come from many outside systems: a freeform list first, then GitHub through `gh`, an internal work system through its own signed-in CLI, and mail and calendars through Office 365 and Microsoft Graph. Each source needs its own way of fetching, and its own auth: some reuse a CLI that is already signed in, others need an OAuth sign-in. Other people should be able to add sources without a new Admiral release, and a broken source must not take the server down.

## Decision

Each order source is a long-lived subprocess that Admiral starts and talks to with JSON-RPC over stdio, as LSP and MCP servers do. A plugin can be written in any language. It owns its auth: it reuses an existing CLI login where there is one, and otherwise asks Admiral to show a sign-in URL or device code in the UI. It can push "orders changed" to Admiral instead of waiting to be polled.

The freeform list is a plugin like any other, not built into Admiral: a single Python 3 file using only the standard library, kept in this repo and installed with Admiral, and added as a source on a fresh install. It is the reference plugin. The methods and messages are decided in [Order source plugin interface](https://github.com/jbuffin/admiral/issues/21).

## Considered options

- **Compiled into Admiral.** The simplest, but only an Admiral release can add a source.
- **A command that prints orders as JSON.** Trivial to write, but it can only be polled, and it can't run a sign-in or describe its settings.
- **Long-lived JSON-RPC subprocess** (chosen).
- **WASM (Extism).** Sandboxed, but that sandbox makes it hard to shell out to `gh` or another CLI or reach the network, which is what these sources do.
- **Existing MCP servers as sources.** They exist for GitHub, Atlassian and Microsoft 365, but they expose general tools, not "list my orders", so each would still need a mapping layer.

## Consequences

- A plugin crash stays in its own process and does not stop the server.
- Admiral needs good documentation for building a plugin, since others will write them. The MVP ships a protocol reference, with the freeform list as its reference plugin; the GitHub plugin becomes the worked example after the MVP.
- Installing Admiral needs `python3`, for the freeform list.
- Plugins run with the engineer's permissions; Admiral runs only the plugins the engineer installs.
