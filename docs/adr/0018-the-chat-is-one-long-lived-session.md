# 18. The chat is one long-lived session

Status: accepted

## Context

The chat pane (rung 8 of autopilot, [#119](https://github.com/jbuffin/admiral/issues/119)) puts AdmiralBot in Admiral's web UI. AdmiralBot is a Claude Code session that Admiral keeps running in tmux, as it does the setup terminal's `_setup`. It is meant to be always on: the engineer says "check such-and-such out" from a phone, and work starts. The chat could be one session that lives as long as Admiral's tmux server, or a new session for each conversation, with a list of past conversations to choose from. The design in [#159](https://github.com/jbuffin/admiral/pull/159) shows one Chat tab on a phone and one panel on larger screens, with no list of conversations.

## Decision

**There is one chat session.** Admiral starts it on its tmux socket, outside the ships, and keeps it running. Every browser sees the same conversation.

- **New chat resets it.** New chat asks first, then clears the conversation. The session's context is reset only this way. There are no saved conversations to go back to.
- **Open terminal shows the session's terminal** through the bridge, as the setup terminal does. Back to chat returns.
- **It is not a ship.** It never shows in open water, the Ships list or the outcome log, and it takes no part in caps, guards or resources. Its Admiral skill can read ships, outcomes and the inbox, and its only write is adding an order (`POST /api/sources/{key}/orders`). Everything it starts goes through an order, so the caps, guards and resources still apply.
- **Its questions stay in the chat.** A question uses the inbox's question card, but it never becomes an inbox item, since an inbox item belongs to a ship.
- **Where it runs and at what model** come from the top-level `chat` key in `config.json`: `fleet` (default: the first fleet with a Claude dock), `model` (default `sonnet`) and `effort` (no value means the harness default). Settings writes it.

## Considered options

- **One long-lived session** (chosen). One place to talk to AdmiralBot from any device, one idle Claude Code process, and nothing to choose before typing on a phone.
- **One session per conversation.** Old conversations stay separate and can be returned to, but it needs a conversation list in the UI, a rule for when idle sessions end, and more idle memory for each one kept.

## Consequences

- Context grows until the engineer taps New chat. A long conversation costs tokens on every turn, and AdmiralBot may lose earlier detail once the harness compacts it.
- New chat clears the conversation for every device at once.
- A dead tmux server ends the chat session as it ends the ships (ADR 0012), and the conversation is lost with it.
