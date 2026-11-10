# AdmiralBot's context: a thin coordinator with a server-owned memory

> Research for the Admiral redesign map, 2026-11-09. Decided since: AdmiralBot launches ships, teams and background sessions directly. Preferences get a Preferences screen (global and per fleet: harness, model, effort, refresh threshold), backed by the ledger and a per-fleet instructions file. Worker cards go to the AdmiralBot chat. Visible history is never wiped, and a reset shows a quiet marker. Workers on email, Slack and logs may launch work, behind the guardrails in `worker-guardrails.md`.

## Short answer

Don't let one chat grow without limit, and don't add a visible bot per campaign. Each fleet gets one AdmiralBot chat. Its context is disposable. What it must remember lives in a per-fleet **ledger** that the server owns. Admiral resets the chat often and refills it from the ledger, and resets are safe because nothing the bot needs is held only in context. Events go to short-lived **worker turns**, not into the chat. Campaigns are driven by the server plus worker turns, and the real work runs in ships. A Commodore ship is its own orchestrator: AdmiralBot records that it delegated the work, then forgets the details.

## Why not one long chat

- Opus 5.5 has a 1M window. Claude Code auto-compacts 1M models at about 967K tokens unless `autoCompactWindow` is set, and **idle compaction** (on by default) compacts a conversation over 200K tokens after about an hour idle.
- Every turn re-sends the whole conversation. At $4/MTok input and $0.20/MTok cache reads, a cached turn at 800K tokens costs about $0.16 before any output. A cold turn after the 1-hour cache expires costs several dollars. The same turns at 100K tokens cost about $0.02 cached and about $0.40 cold.
- A compaction summary is lossy and written by the model. Instructions given only in conversation don't survive it. Only CLAUDE.md, auto memory, skills and `SessionStart(compact)` hook output are re-injected. A commitment kept only in chat ("I'll start #231 when #230 merges") can disappear.
- OpenClaw does the same: disposable sessions (`session.reset` daily or idle), Markdown memory (`MEMORY.md`, daily notes) with a "memory flush" turn before compaction, and a fresh session per cron run. Anthropic's context-engineering guidance names the same tools: compaction, notes, and sub-agents.

## Design

```text
            engineer (desktop / phone)
                     │  Admiral's own conversation log (what the UI shows)
                     ▼
 ┌──────────── fleet: Work ─────────────────────────────────────────┐
 │  AdmiralBot chat (_chat-work, Opus 5.5 high, tmux TUI)           │
 │    context = skill + BRIEF + talk since the last reset            │
 │        │ admiral … (CLI fence)               ▲ SessionStart hook  │
 │        ▼                                     │ cats brief.md      │
 │  Admiral server ── ledger (commitments, campaigns, routines,      │
 │        │            delegations, claims, notes) → brief.md        │
 │        │ events (ship sailed off, routine fired, mail)            │
 │        ▼                                                          │
 │  worker turns (claude -p, Sonnet/Haiku, one-shot, fenced)         │
 │        │ ledger writes, orders/launches, chat cards               │
 │        ▼                                                          │
 │  ships: one per campaign child, or a Commodore ship per slice     │
 │         (Commodore lead + teammates, its own state.md)            │
 └───────────────────────────────────────────────────────────────────┘
   fleet: Home has the same stack, with its own dir, config dir and ledger
```

### What lives where (per fleet, under `$ADMIRAL_HOME/chat/<fleet>/`)

| Kept in | What |
| --- | --- |
| **Ledger** (server-owned, `ledger.json` or SQLite) | Commitments (id, text, trigger or due, source message, status); campaigns (parent issue, mode rules, HITL slot); routines; delegations (ship, order, "tell the engineer when…"); claims; a capped list of notes (preferences) |
| **`brief.md`** | Rendered by the server from the ledger and live state on every change, kept under 10,000 characters (the cap on `SessionStart` context). Contains open commitments, active campaigns as one line each, pending chat questions, ships waiting on the engineer, events since the bot's last turn, and notes |
| **`conversation.jsonl`** | The visible chat as Admiral records it: user and assistant text from the transcript tail, worker cards, and reset markers. It is never fed back to the model whole |
| Chat context | The skill, the brief and the current exchange only |

The bot writes only through new `admiral` commands (`admiral bot commit|done|note`, `admiral campaign …`, `admiral claim …`), so ADR 0020's fence still holds: no `Edit` and no `Write`. The skill's rule is **write at the moment you promise**: no promise to the engineer without an `admiral bot commit`.

### When and how it resets

Admiral, not the model, decides when to reset. It resets only while the chat is idle, with no turn running and no chat question open:

1. **Soft threshold:** context over about 150K tokens and idle for 10 minutes or more.
2. **Cold cache:** idle for over 1 hour with context over 100K tokens. The next turn would pay a full cache write anyway, so a fresh brief is cheaper.
3. **Daily:** at a quiet hour, the OpenClaw-style backstop.
4. **Hard backstop:** `autoCompactWindow` of about 300K in the chat's `--settings` bundle. A `PreCompact` hook prints "keep commitment ids, campaign ids, open questions" as summary instructions, and a `PostCompact` hook stores `compact_summary` for auditing.

A reset runs in three steps:

- **Flush:** a hidden turn: "record any promise or decision not yet in the ledger." This matches OpenClaw's `memoryFlush`.
- **Clear:** `/clear`.
- **Re-hydrate:** `admiral hook SessionStart` (sources `clear`, `compact` and `startup`) cats `brief.md` to stdout. The hook reads a file and never calls the server, so ADR 0007 and ADR 0016 hold.

New chat still forgets the conversation; the ledger survives it. A launch doesn't trigger a reset; it is a handoff point that makes the next reset safe.

### Events: worker turns, not the main chat

The server handles what it can compute: ready children, blocked-by and caps. An event that needs judgment starts a **worker turn**. A worker turn is a one-shot `claude -p` run with the same fence, given the brief, the event and the campaign file. Worker turns run on Sonnet 5.5, or on Haiku 5.5 for inbox and log triage. They never use `--resume`, so ADR 0012's "never resumes" holds. Avoid `--bare`, which needs an API key and skips the subscription login. Each worker turn ends in ledger writes, an order or launch, or a **card** in the chat ("#230 merged; grilling #231 is ready. Start it?"). The main bot sees the card only through the brief, as a digest. Workers that read outside text (mail, Slack, logs) can only propose, never launch, until the engineer approves.

### Campaigns

There is no long-lived campaign bot. A campaign's state is its GitHub tree plus its ledger row. The server computes the ready set and enforces **one with-you (HITL) ticket at a time**. When a child sails off, a worker turn re-reads the tree and launches the next AFK ticket, or hands a build slice to a Commodore ship. A Commodore lead is scoped to one mission and survives compaction through `state.md`. A weeks-long campaign would outgrow it, so Commodore runs per slice, not per campaign. AdmiralBot's part is one delegation line in the ledger.

### Model and cost

- **Chat:** Opus 5.5. Pass `--effort high` explicitly, since Opus 5.5 defaults to `medium`.
- **Worker turns:** Sonnet 5.5 ($2/$10 per MTok) for campaign judgment. Haiku 5.5 ($0.10/$0.50) for classifying inbound noise.
- **Caps:** each worker turn gets `--max-budget-usd`, and each routine gets a rate limit.

### Failure modes

| Risk | Guard |
| --- | --- |
| A commitment lost on reset | Write-at-promise rule, the flush turn, the brief, and the `PreCompact` instructions. The ledger is the truth, not the summary |
| Duplicate launches | An idempotency key on order and launch (`campaign:<id>:child:<n>`). The server refuses the key while a live ship holds it |
| Two actors on one ticket | A server-side claim per ticket (chat, worker or ship), released when the ship ends, plus the GitHub assignee as the external claim. Worker turns for one campaign run one at a time; events queue |
| Reset during a question | Admiral never resets while a question is open or a turn is running |
| Fleets crossing | One chat, config dir, ledger and worker pool per fleet. The CLI carries `ADMIRAL_FLEET`, and the server refuses anything that names another fleet |

## Open questions for the owner

1. Should the ledger hold free-form "notes" (preferences), or should those go in a per-fleet CLAUDE.md that the engineer edits?
2. Can the bot launch directly (with an idempotency key), or only add orders that autopilot launches? This is ADR 0020's only write.
3. Should worker cards appear in the chat or in Needs you? Do they notify the phone?
4. Which reset thresholds (150K/1h/daily), and should the engineer see a "context refreshed" marker?
5. Does New chat also wipe the visible history, or only the model's context?
6. May inbound-channel workers ever launch on their own, for example a routine with prior approval?

## ADRs and glossary this touches

- **ADR 0018:** one chat becomes one per fleet; automatic resets join New chat.
- **ADR 0019:** the UI serves Admiral's `conversation.jsonl` across clears; `SessionStart` re-hydrates.
- **ADR 0020:** new write commands (ledger, claims, launch), and the hook reads `brief.md`.
- **ADR 0012:** still holds (no resume).
- **ADRs 0007, 0015, 0016:** hold.
- **New ADRs:** "AdmiralBot's memory is a server-owned ledger", "events go to worker turns", and "campaigns are driven by the server, not a bot".
- **Glossary:** update the AdmiralBot entry (per fleet, disposable context). Add proposed terms **Ledger**, **Brief** and **Worker turn** (naval alternatives: Logbook, Watch). The ledger's "claim" needs a definition beside wayfinder's.

## Sources

- Claude Code: <https://code.claude.com/docs/en/context-window>, /hooks, /model-config, /settings-reference, /headless, /memory, /costs (all under <https://code.claude.com/docs/en/>)
- Model IDs, prices and API compaction (`compact-2026-01-12`, 150K default trigger), context editing and the memory tool: the claude-api skill (cached 2026-11-03), and <https://platform.claude.com/docs/en/build-with-claude/compaction> (not opened)
- Anthropic, Effective context engineering, <https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents>
- OpenClaw: <https://docs.openclaw.ai/concepts/session>, <https://docs.openclaw.ai/concepts/memory>, <https://docs.openclaw.ai/concepts/compaction>
- Grok Bot: <https://x.ai/bot/guides/grok-bot-101> (says nothing about memory); per-Bot memory and routines from secondary coverage at <https://vpsranking.com/news/ai/ai-2026-09-03-xai-grok-bot-persistent-agents/> (not opened). The official x.ai Grok Bot design page was not found or opened.
