# 24. AdmiralBot's memory is a server-owned ledger, and Admiral decides context resets

Status: accepted

## Context

AdmiralBot lives for weeks ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)). Under [ADR 0018](0018-the-chat-is-one-long-lived-session.md) its context grows until the engineer taps New chat, every turn costs more, and the harness's compaction can drop a promise made days ago. The research in [#218](https://github.com/jbuffin/admiral/issues/218) ([note](../research/admiralbot-context.md)) pointed to a thin coordinator whose memory lives outside the model; [#223](https://github.com/jbuffin/admiral/issues/223) decided its shape.

## Decision

**What AdmiralBot must remember lives in a per-fleet ledger the server owns. The server renders a brief from it and from live state. Admiral resets the model's context on its own schedule and refills it from the brief.**

- **The ledger holds two kinds of entry.** **Commitments**: what AdmiralBot agreed to do or watch for. **Notes**: facts the engineer told it to remember, about 30 per fleet. Campaigns, routines and claims are their own server records, and there are no delegation entries: a ship launched via AdmiralBot already carries that stamp.
- **Where it lives.** `$ADMIRAL_HOME/chat/<fleet>/ledger.jsonl`, an append-only event log (added, fired, done, cancelled) folded in memory, each event stamped via AdmiralBot or via the engineer. It has the shape of the outcome log ([ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md)).
- **Instructions are not notes.** Each fleet's **instructions**, about 2,000 characters on Preferences, are the engineer's. AdmiralBot reads them and can never write them: the fence is the MCP server and the actor token, and a ship AdmiralBot launches acts as the engineer on loopback (ADR 0023: ships are not fenced), so it is outside that guarantee. Preferences also lists the notes; the engineer can delete one or promote it to the instructions.
- **Commitments.** Written only on the engineer's instruction in the chat, and quoted back. A commitment has a trigger from a fixed list Admiral can observe (a ship sailed off or anchored, a PR merged or closed, an issue closed, a time, or none) and one action:
  - **launch**: the full launch spec is stored when the commitment is made. When the trigger fires, the server runs it with no model turn, waiting for cap room and resources ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)).
  - **remind**: the server posts a card into the chat.
  - A launch that can't run (the issue is closed, the dock was removed, a live ship holds the idempotency key) becomes a remind card that says why, and is marked done. A commitment never retries on its own. If its subject closes without the event (a PR closed unmerged, a ship dismissed), it is dropped with a card. One with no trigger, older than 14 days, shows once as a "still want this?" card. Nothing is dropped silently, and the engineer can cancel any commitment from the ledger list.
- **The brief.** The server renders `chat/<fleet>/brief.md` on every change, under 10,000 characters, and a `SessionStart` hook loads it by reading the file. In priority order, cut from the bottom: the instructions; open commitments with ids and ages; AdmiralBot's waiting chat question; live ships it launched and ships in the fleet waiting on the engineer; active campaigns and routines, a line each; notes; the last six or so messages before the reset.
- **Events between turns.** A `UserPromptSubmit` hook prepends a short "since your last turn" digest from a file the server writes. Cards show in the chat for the engineer, and the model is never woken just to narrate. No hook calls the server, so [ADR 0007](0007-needs-you-comes-from-harness-hooks.md) and [ADR 0016](0016-answers-to-inbox-items-are-files-beside-the-item.md) hold.
- **Resets.** Admiral resets only while the chat is idle, with no turn running and no chat question open: when context is over the fleet's threshold (150K by default) after 10 idle minutes; when idle over an hour with context over 100K; daily at a quiet hour (04:00 local); and as a backstop, `autoCompactWindow` of about 300K with a `PreCompact` hook that keeps commitment ids. Only the threshold is on Preferences. Each reset is a **flush** (a hidden turn that records any promise or decision not yet in the ledger), then `/clear`, then the brief. The chat shows a quiet "context refreshed" marker.
- **Visible history is Admiral's.** Admiral keeps `chat/<fleet>/conversation.jsonl` (text, cards and reset markers), rotated monthly and kept forever, never fed back to the model. The UI serves it across resets instead of the transcript tail alone. **New chat** is a manual reset, the same flush and clear with a "New chat" marker. Commitments survive it.

## Considered options

- **A server-owned ledger with Admiral-decided resets** (chosen). Promises survive any reset, the context stays small and cheap, and the engineer can see and cancel what AdmiralBot has promised.
- **One long chat with the harness's compaction** ([ADR 0018](0018-the-chat-is-one-long-lived-session.md)). Nothing to build, but compaction decides what is kept, and a promise can vanish without anyone noticing.
- **Notes and instructions in one list AdmiralBot writes.** Simpler, but AdmiralBot could then loosen its own guidance.

## Consequences

- This amends [ADR 0018](0018-the-chat-is-one-long-lived-session.md): New chat is no longer the only reset, and visible history outlives it. It amends [ADR 0019](0019-the-chat-is-carried-by-its-transcript-and-tmux.md): the UI serves Admiral's conversation log, and `SessionStart` rehydrates from the brief.
- A commitment fires with no model turn, so a stored launch spec can go stale. It is checked when it fires and turns into a remind card when it can't run.
- The flush is a model turn, so a reset costs one short turn.
