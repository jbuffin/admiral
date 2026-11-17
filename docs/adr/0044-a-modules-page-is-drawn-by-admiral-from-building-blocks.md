# 44. A module's page is drawn by Admiral from building blocks

Status: accepted

## Context

[ADR 0006](0006-order-sources-are-subprocess-plugins.md) lets a plugin bring a tracker in only as orders. Some trackers don't fit: their tickets have steps the engineer claims, answers and closes, and the code work lives on GitHub issues the ticket links to. [#715](https://github.com/jbuffin/admiral/issues/715) asked for a second kind of plugin, a module, with its own page in the fleet's sidebar and actions the engineer can take there. A plugin is someone else's code fed by upstream text, and the page sits beside ships that hold the engineer's repos and logins.

## Decision

**A module declares a page in `initialize` and sends data only. Admiral draws the page from a fixed set of building blocks and treats everything the module sends as untrusted.**

- **Building blocks, never HTML.** The page is ordered groups of rows (title, subtitle, chips with a tone from a fixed set, a reason line, one main action), a detail view in the side pane (link lists, step rows, key/value lines), forms (choice, checklist, duration) and Admiral's own filter. A module never sends markup, script, styles or colors.
- **Two kinds of action.** A module action is run by the module upstream, on a click, after a yes/no or from a form the module fills when it opens; its answer is a message and an optional link. A built-in action is carried out by Admiral: open an `http(s)` link, launch a ship on a GitHub issue the module names (normal launch dialog and routing; anything that isn't a GitHub issue is refused), or message AdmiralBot. A module never launches a ship on its own rows.
- **AdmiralBot gets fields only.** A module declares its message kinds once, each with a fixed job sentence. An action names a kind and passes a row's reference, its title cut to one line and fenced as quoted data, and an `http(s)` link. Admiral writes the message, shows the whole of it in the confirm and posts it as the engineer's, marked with its origin. No other upstream text, such as a ticket's description, reaches AdmiralBot, in line with [ADR 0025](0025-outside-events-are-quarantined.md).
- **Limits.** Admiral caps rows, groups, chips, actions, line length and detail size, accepts only `http(s)` URLs, and drops anything over a limit whole while keeping the last good page.
- **Its own count.** A module's waiting count shows on its sidebar entry only. Its rows stay out of the Inbox and the attention count.

## Considered options

- **Building blocks drawn by Admiral** (chosen).
- **Plugin HTML in a sandboxed iframe.** Any page a module wants, but a second look and feel, no iPad touch guarantees, and a channel for upstream text to dress itself up as Admiral.
- **Rows shown as orders.** No new protocol, but the tracker's tickets aren't things to launch from, and there is nowhere to put the engineer's actions.
- **Free text to AdmiralBot.** Lets a module word its own requests, but upstream text would then reach a bot that can launch ships.

## Consequences

- A module needing a block Admiral lacks waits for an Admiral release; new blocks and field types are added as modules need them.
- The protocol stays at version 1, since the change is additive, and an older Admiral ignores the page.
