# 29. The bridge answers simple prompts by sending the harness's keys

Status: accepted

## Context

Today the bridge is the ship's terminal ([ADR 0011](0011-the-bridge-follows-the-latest-active-browser.md)). The redesign opens it on the ship's conversation instead ([#208](https://github.com/jbuffin/admiral/issues/208)), read from the harness through one adapter per harness ([ADR 0030](0030-ship-conversations-come-from-a-first-party-adapter-per-harness.md)). A ship that needs the engineer often asks a plain question or wants a permission, and sending the engineer to a terminal for each one defeats a conversation view, above all on a phone.

[ADR 0011](0011-the-bridge-follows-the-latest-active-browser.md) has no Approve or Deny buttons, because harness prompts are menus and a single key sent blind can answer the wrong thing or land as text. Only Copilot's server offers a supported way to answer through an API, and an observing client that answers would act for the engineer. [#232](https://github.com/jbuffin/admiral/issues/232) decided what the bridge shows and how it answers.

## Decision

**The bridge opens on a Conversation tab. Its message box pastes into the boarded agent's pane the way the chat does. A single-choice question and a permission prompt are answered in place by sending the harness's own keys, and only while hook state still matches the card. Everything else shows a card that points to the terminal. The bridge never switches tabs by itself.**

- **Tabs.** Conversation (the default, always opened first, no remembered tab), Terminal (today's bridge, unchanged), Changes and PR. Changes is a read-only diff of the ship's checkout against its merge-base with the base branch, uncommitted work included: a file list and a unified diff, with no commenting. PR shows the chips (review, checks, mergeable, conflicts) and a moored ship's actions ([#230](https://github.com/jbuffin/admiral/issues/230)), or "No pull request yet".
- **A turn.** User messages show as typed and assistant text renders as Markdown. A finished turn folds its tool calls into one collapsed row ("Worked for 6m · read 9 files, ran 2 commands"). The running turn lists each tool call as it lands, one line each; tapping a line shows its output, capped at 200 lines with "Show all". A subagent run is one tool-call line, and a compaction marker is a quiet divider. While hooks say working, a "Working · 2m" row sits at the bottom. Text copies by selection or long-press; messages have no other actions.
- **The message box** sends by the chat's route ([ADR 0019](0019-the-chat-is-carried-by-its-transcript-and-tmux.md)): a bracketed paste and Enter, control characters other than LF and tab stripped, at most 16 KiB. Sending clears needs-you, as bridge input does today. It stays enabled while the ship works; the harness's own queueing applies and Admiral keeps no queue. A sent message shows at once as a dimmed "sent" bubble and becomes a real message once the harness records it. On a moored ship the box wakes it (#230). On an anchored ship it is disabled with "Exited with error, read only". **Interrupt** (Esc) sits beside it while the ship works; Ctrl-C and the other keys stay in the Terminal tab's key bar.
- **Answering in place.** A per-harness key map sends the TUI's keys for two cases:
  - A **single-choice question**, from the adapter: one button per option, which sends its number.
  - A **permission prompt**, from hook state: **Allow once** and **Deny**. The card names the tool from `@admiral-reason` and shows the pending tool call from the conversation when it is there.
- **Gated on hook state.** A button sends keys only while the hook state still matches its card. If the state has moved on, the card goes away.
- **Everything else goes to the terminal.** Multi-select, "Other" with free text, "always allow", a question with no options, and any stop whose reason has no card (trust dialog, idle, bell) get a card that says "Answer this in the terminal" with **Open terminal**. The same kind of card covers "Admiral can't read this conversation" ([ADR 0030](0030-ship-conversations-come-from-a-first-party-adapter-per-harness.md)) and "Starting…" before anything is recorded.
- **Pane size.** Only the Terminal tab joins the bridge WebSocket and can become the active browser. Conversation, Changes and PR never resize the pane, so reading on a phone doesn't shrink the Mac's terminal.
- **Flotilla.** Chips above the tabs: the ship, then each crew boat with its role. Picking a crew boat switches Conversation, Terminal, the message box and the answer cards to that boat, reading its recorded transcript path. Changes and PR stay the ship's. Subagents get no chip. An exited crew boat's chip stays and reads as exited.
- **Stop ship** asks for confirmation, then ends the session and its crew boats. The ship leaves as a Dismiss does: it neither moors (even with an open PR) nor stays anchored. It records a `stopped` outcome. AdmiralBot's stop write does the same.
- **Transport.** `GET /api/v1/ships/<ship>/conversation` returns the last 200 items, and `?before=<item id>` pages back as the engineer scrolls up. A crew boat uses `…/crew/<name>/conversation`. New items arrive on the main WebSocket as a `conversation` message, sent only to browsers that have that ship or boat open. A moored ship's rounds come as one list with divider items. The Terminal tab's bridge protocol is unchanged.
- **Peek** in the Inbox uses the same conversation view, cut to the last turn and scrolled to the bottom, with the same answer cards and message box, plus **Open bridge**.
- **The Ships list tail** comes from the conversation's latest item: a pending question's text, else the last tool call's line, else the first line of the last assistant text. It is blank when nothing is readable, and never scraped from the terminal.

## Considered options

- **Keys from a per-harness map, gated on hook state** (chosen). It works on every harness and through the same TUI the engineer would use, and a card that no longer matches the state can't send anything.
- **Answering through each harness's API.** Only Copilot's server offers it, and Admiral's observing clients register no handlers so they can never answer for the engineer ([ADR 0030](0030-ship-conversations-come-from-a-first-party-adapter-per-harness.md)).
- **No answer buttons** ([ADR 0011](0011-the-bridge-follows-the-latest-active-browser.md)). Safe, but every question means switching to the terminal.
- **Switching to the terminal when a ship needs you.** A tab that changes under the engineer's finger is worse than a card with **Open terminal**.

## Consequences

- This amends [ADR 0011](0011-the-bridge-follows-the-latest-active-browser.md): only the Terminal tab joins the bridge socket and becomes the active browser, and the Conversation tab has answer buttons for the two cases the key map covers. The terminal itself is unchanged.
- It amends [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md) with a `stopped` event.
- A harness release can change its menus. Each harness's key map gets one test against a recorded TUI, and a case the map doesn't cover goes to the terminal.
- The structured view answers through keystrokes, which is surprising. The hook-state gate is what keeps a stale card from sending keys into a prompt the engineer never saw.
- The conversation endpoints and the `conversation` WebSocket message belong in the API docs, not in `docs/bridge-protocol.md`.
