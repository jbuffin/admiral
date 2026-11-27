# 51. What a ship needs posts a need card in its fleet's chat

Status: accepted

## Context

A client that shows only AdmiralBot has to let the engineer answer whatever a ship needs: a question, an approval, a finished turn's next prompt. Today those go to the Inbox and nowhere else. [ADR 0042](0042-a-ships-result-comes-back-as-a-card-and-a-report-turn.md) and [ADR 0043](0043-an-idea-in-the-chat-becomes-a-pr-with-no-questions-on-the-way.md) say AdmiralBot neither relays nor answers them. AdmiralBot can already list the Inbox and, on the engineer's word, answer a question with `answer_item` or give a ship its next prompt, but nothing brings a need into the chat, and it has no tool for a permission prompt. [#938](https://github.com/jbuffin/admiral/issues/938) asked how a need should reach the engineer through AdmiralBot.

## Decision

**When a ship needs the engineer, the server posts a need card in the chat of the ship's fleet, with no model turn. The card is the Inbox item or stop itself: a tap answers it exactly, and a reply in words goes to AdmiralBot in an engineer turn. AdmiralBot never answers a need on its own.**

- **What posts a card.** Every Inbox item (a ship's question, a pending review, finding cards, a routine proposal, an allow rule proposal, a campaign draft) and every needs-you stop (a permission prompt, a finished turn waiting for its next prompt, a session that has not started), for every ship in the fleet, however it was launched. Ready to merge posts a card for every ship, not only ones AdmiralBot launched.
- **Where.** The chat of the ship's own fleet. Cards for chat questions are not added, since the question is already in that chat. The Inbox and its count still span every fleet.
- **Cards that only show.** Ready to merge links its PR, and a session that has not started says to open its Terminal tab. Neither has buttons: AdmiralBot never merges, and folder trust is answered at the terminal.
- **A tap is the engineer's own answer**, the same as answering in the Inbox: it writes the answer file or sends the harness's keys. It is not marked via AdmiralBot.
- **A reply in words.** A reply under a card goes to AdmiralBot in an engineer turn with that card's item attached. A message that names no card ("yes to the deploy one") is matched against what is waiting. AdmiralBot acts only when exactly one fits, and asks a chat question when several fit or none does. It answers with `answer_item`, `next_prompt` or a new `answer_stop`, which answers a ship's permission or question menu through `POST /api/ships/<session>/answer`. Each is refused in a report turn by the turn gate ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)), sends only the choice the engineer gave, and is marked via AdmiralBot.
- **AdmiralBot answers nothing by itself.** It may suggest a choice ("I'd pick Squash; say go"). Autopilot already takes the low-stakes calls, so what reaches the Inbox is high-stakes, and a permission prompt is a guard decision.
- **One truth.** The answer file, or the stop clearing, decides. A card is bound to its item or to the one stop it was posted for, checked again when it is answered, so a ship that has moved on never gets keys meant for an earlier prompt. The first answer wins. The card updates in place to Answered (with where: the chat, the Inbox), Settled in the terminal, Moved on or Removed, then collapses to one line. A late tap or tool call answers "already answered" and changes nothing.
- **Seen and pushes don't change.** Posting a card marks nothing seen; seen stays the Inbox's. A need pushes once, as it does today. A client that opens the chat from a push opens it at the card, and a reply from the notification answers the card's item.

## Considered options

- **A need card the server posts** (chosen). Answers map exactly, no model turn is spent on a need, the web chat gets the same, and it follows the result cards of ADR 0042 and ADR 0043.
- **AdmiralBot relays each need in a model turn.** A turn per need, and a paraphrase can drift from the item's choices, so a reply can map to the wrong answer.
- **A client shows Inbox items beside the chat, and Admiral doesn't change.** It breaks a view that is AdmiralBot alone, and the web chat gets nothing.
- **AdmiralBot answers low-stakes needs itself.** Autopilot already does that before anything reaches the Inbox; what is left is the engineer's call.

## Consequences

- Amends ADR 0042 and ADR 0043: a ship's need still queues no report turn, but it now posts a need card in the chat, and AdmiralBot answers one on the engineer's word. Ready to merge posts a card for every ship.
- Amends ADR 0023: `answer_stop` joins AdmiralBot's writes.
- A busy fleet's chat carries a card per need; answered cards collapse so it stays readable.
