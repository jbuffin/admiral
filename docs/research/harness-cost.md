# What cost and usage does each harness report?

Research for [#245](https://github.com/jbuffin/admiral/issues/245), on the redesign map [#206](https://github.com/jbuffin/admiral/issues/206). It builds on [harness-transcripts.md](https://github.com/jbuffin/admiral/blob/research/harness-transcripts/docs/research/harness-transcripts.md) (#231) and feeds the worker spend cap in [worker-guardrails.md](worker-guardrails.md) (#225). November 2026. Versions on this Mac on 2026-11-10: Claude Code 2.1.322, codex-cli 0.166.0, GitHub Copilot CLI 1.0.109. Codex source read at `openai/codex` main, commit `a2b7c4a6`; Copilot SDK at `github/copilot-sdk` main, commit `8aed714`.

Question: for a running or finished session, what does each harness report about tokens, dollars, premium requests or credits, and plan quota; where and when; does it count subagents and crew boats; and what does "cost" mean on a subscription?

Marks: **confirmed** means a primary source says so (doc URL, source `file:line`, `--help`). **observed** means it was seen in this Mac's own session files. **unconfirmed** means nothing settles it. Codex source is written `codex:<path>:<line>`, relative to `https://github.com/openai/codex/blob/a2b7c4a655a124c3e798b7744c5f83397eb2ada3/codex-rs/`. Copilot SDK paths are relative to `https://github.com/github/copilot-sdk/blob/8aed71413479f146592971bb909785ddbab13ac2/`.

## Summary

| | Claude Code | Codex CLI | Copilot CLI |
| --- | --- | --- | --- |
| **Tokens** | Per API response, in the transcript; per session, in the status line input and the exit line | Per model response and per thread, in the rollout's `token_count`, the `PostModelRequest` hook and app-server events | Per model call, in `events.jsonl` (`session.usage_record`); per session at exit (`session.shutdown`) |
| **Dollars** | Yes, an estimate at list price, computed by the client | **No**: no dollar figure anywhere | AI credits per call and per session (`totalNanoAiu`); 1 credit is $0.01 |
| **Premium requests** | n/a | n/a | Yes: per-call multiplier and session total (legacy billing) |
| **Plan quota** | Status line `rate_limits` (5-hour, 7-day, %) for Pro/Max/Ultra | `rate_limits` in every `token_count` (primary/secondary window %, credits, plan type) | Not in the session file; `account.getQuota` over the SDK only |
| **Live, without scraping** | Status line command (every assistant message) or the transcript | `PostModelRequest` hook, rollout, or app-server `thread/tokenUsage/updated` | `events.jsonl`, one record per model call |
| **At exit** | `cost-state` line in the transcript (observed) | Last `token_count` | `session.shutdown` with totals |
| **Subagents** | Session cost includes them; per-message usage is in separate subagent files | Each subagent is its own thread with its own totals; not rolled into the parent | Included in the same file and the session totals |
| **Hooks with usage** | None with session totals; `PostToolUse` on `Agent` has the subagent's last request only | `PostModelRequest` carries `token_usage` and `agent_id` | None |

A fair "cost" for Admiral is tokens first, then a dollar figure labelled by its basis: Claude's own estimate, Copilot's AI credits, and for Codex a figure Admiral would have to price itself. On a subscription the binding limit is the plan quota, which is per account, not per ship.

## Claude Code

### What it reports and where

| Source | Tokens | Dollars | Quota | When | Subagents |
| --- | --- | --- | --- | --- | --- |
| Transcript `assistant` lines, `message.usage` | `input_tokens`, `output_tokens` (with `thinking_tokens`), `cache_creation_input_tokens` (5m / 1h split), `cache_read_input_tokens`, `service_tier`, `speed`, `server_tool_use` | No | No | Per API response, as each content block is written (observed) | No; subagents write `…/subagents/agent-<id>.jsonl` with their own `usage` (observed) |
| Transcript `cost-state` line | `modelUsage` per model, with token classes | `totalCostUSD`, per-model `costUSD` | No | When the process exits normally | Unconfirmed; the SDK's equivalent totals include them |
| Status line command input | `context_window.current_usage`, `total_input_tokens`, `total_output_tokens` (context only, not a session total) | `cost.total_cost_usd` | `rate_limits.five_hour` / `seven_day` `used_percentage` and `resets_at`; `spend_limit` behind a gateway | Session start, each new assistant message, `/compact`, resets; 300 ms debounce | Not stated on the page; the same figure as `/usage`, which the SDK says includes subagents |
| `claude -p --output-format json` result | `usage` (main loop only), `modelUsage` (all) | `total_cost_usd` | No | At exit of the print run | `total_cost_usd` and `modelUsage` yes, `usage` no |
| OpenTelemetry | `claude_code.token.usage` by type | `claude_code.cost.usage`; `api_request` event `cost_usd` | No | After each API request; metrics export every 60 s, events every 5 s | Yes: `query_source` `subagent`, `agent.name`; events carry `agent_id` |
| Hooks | `PostToolUse` on `Agent`: the subagent's final request's `usage` and `totalTokens` | `SessionStart` (resume) and `PreModelSwitch`: `estimated_cache_write_usd` for re-sending context | No | Per event | Only that final request |

Sources:

- Transcript usage fields and the 1-hour cache split: observed in this Mac's transcripts. Blocks of one response repeat the same `usage`; none of 1,237 lines had `output_tokens` ≤ 4, so the transcript seems to hold final counts, not the SDK's `message_start` placeholder ([cost-tracking#read-output-tokens-from-the-result-message](https://code.claude.com/docs/en/agent-sdk/cost-tracking)). Unconfirmed: no doc says which the transcript holds.
- `cost-state` line: observed (88 of them in the last 80 transcripts, with `totalCostUSD`, `totalAPIDuration`, `totalLinesAdded`, `modelUsage.<model>.costUSD`). The docs confirm the mechanism without naming the line: "Claude Code saves the session's totals to its transcript when the process exits normally and restores them when a later call resumes or forks the session" ([cost-tracking#accumulate-costs-across-multiple-calls](https://code.claude.com/docs/en/agent-sdk/cost-tracking)). The transcript format is internal and "changes between versions" ([sessions](https://code.claude.com/docs/en/sessions#where-transcripts-are-stored)).
- Status line fields and triggers: confirmed, [statusline#available-data](https://code.claude.com/docs/en/statusline) ("`cost.total_cost_usd`: Estimated session cost in USD, computed client-side at list price … Resets to $0 when `/clear` starts a new session"; "`rate_limits`: appears only for claude.ai Pro, Max, and Ultra subscribers, or behind a Claude apps gateway that sets a spend limit"). `context_window.total_*` is the current context, not a running total. The `prompt_cache` object "doesn't count subagent requests".
- `/usage` and the status line show the same total: confirmed, [costs#using-the-usage-command](https://code.claude.com/docs/en/costs).
- `-p` JSON and subagents: confirmed, [headless](https://code.claude.com/docs/en/headless) ("the response payload includes `total_cost_usd` and a per-model cost breakdown") and the table in [cost-tracking#get-the-total-cost-of-a-query](https://code.claude.com/docs/en/agent-sdk/cost-tracking) (`usage` excludes subagents; `total_cost_usd` and `modelUsage` include them).
- OpenTelemetry: confirmed, [monitoring-usage](https://code.claude.com/docs/en/monitoring-usage).
- Hooks: confirmed, [hooks](https://code.claude.com/docs/en/hooks): the `Agent` `PostToolUse` fields "cover the final request only", and background subagents return no usage at all; `SessionEnd` and `Stop` carry no cost.

### Subscription versus API

- The dollar figure is "computed locally from token counts at list price" and is an estimate. For "Pro, Max, and Ultra subscribers … the session cost figure isn't relevant for billing purposes". Subscribers see plan usage bars instead. Confirmed: [costs](https://code.claude.com/docs/en/costs).
- Team and Enterprise seats draw from "a per-seat allowance that resets on a rolling five-hour window and a weekly window", and "Usage inside the seat allowance isn't metered in dollars". Usage credits past the allowance are in dollars. Confirmed: same page.
- So on a subscription the status line still gives a dollar figure, which means "what this would cost on the API", and `rate_limits` gives the real constraint as a percentage per account.

### Getting it into Admiral

- **Status line**: the only live source of the session total and the quota windows. Admiral already launches Claude with `--settings` for its hooks (`internal/launcher/hookargs.go`), so it could add a `statusLine` that records the JSON and prints the engineer's own status line. Cost: a flag setting replaces the engineer's `statusLine`, so Admiral's command would have to run theirs and pass its output through. The status line also hides during permission prompts and doesn't refresh while idle unless `refreshInterval` is set ([statusline](https://code.claude.com/docs/en/statusline)).
- **Transcript**: per-response tokens, already tailed for the chat (ADR 0019). Admiral would price them itself, and add the subagent files. The `cost-state` line gives an exact match to `/usage`, but only at exit.
- **OpenTelemetry**: per-request cost with subagent attribution, at a 5 s event interval. It needs an OTLP receiver in Admiral and `CLAUDE_CODE_ENABLE_TELEMETRY` plus exporter variables set per ship. Not probed.

## Codex CLI

### What it reports and where

| Source | Tokens | Dollars | Quota | When | Subagents |
| --- | --- | --- | --- | --- | --- |
| Rollout `event_msg` `token_count` | `info.total_token_usage` and `last_token_usage`: `input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `total_tokens`; `model_context_window` | No | `rate_limits`: `primary` / `secondary` (`used_percent`, `window_minutes`, `resets_at`), `credits` (`has_credits`, `unlimited`, `balance`), `individual_limit`, `spend_control_reached`, `plan_type`, `rate_limit_reached_type` | After each model response and each rate-limit update; persisted | Per thread: each subagent has its own rollout |
| `PostModelRequest` hook | `token_usage` for the attempt | No | No | After each model request attempt, retries included | Carries `agent_id` and `agent_type` |
| App-server notifications | `thread/tokenUsage/updated` (`threadId`, `turnId`, `tokenUsage`) | No | `account/rateLimits/updated`; `account/rateLimits/read` on demand | Live | Per thread |
| `codex exec --json` | `turn.completed` `usage` (the same five classes) | No | No | Per turn | Unconfirmed |
| `/status` in the TUI | Shown on screen | No | Remaining limits | On demand | n/a |

Sources:

- `TokenUsage`, `TokenUsageInfo`, `TokenCountEvent`, `RateLimitSnapshot`, `RateLimitWindow`, `CreditsSnapshot`: confirmed, `codex:protocol/src/protocol.rs:2545-2715`. A real line, with `plan_type: "team"`, a 300-minute primary window and a 10,080-minute secondary window: observed.
- `token_count` is sent after every usage or rate-limit update: confirmed, `send_token_count_event` called from `update_token_usage_info` and `update_rate_limits` (`codex:core/src/session/mod.rs:5647-5872`). It is persisted to the rollout: confirmed in [harness-transcripts.md](https://github.com/jbuffin/admiral/blob/research/harness-transcripts/docs/research/harness-transcripts.md) from `codex:rollout/src/policy.rs`.
- `PostModelRequest`: confirmed in source (`codex:hooks/src/events/model_request.rs:19-80`, "Observation-only lifecycle hooks") and in the [hooks docs](https://learn.chatgpt.com/docs/hooks): "`token_usage`: Token counts for the attempt when available, otherwise `null`"; "Hook output is diagnostic only". The string is in the 0.166.0 binary on this Mac (observed). Not probed live.
- App-server methods: confirmed, `codex:app-server-protocol/src/protocol/common.rs:1430`, `:2075`, `:2116`; the notification type is in `codex:app-server-protocol/src/protocol/v2/thread.rs:2066`.
- `codex exec --json` `TurnCompletedEvent { usage }`: confirmed, `codex:exec/src/exec_events.rs:50-75`.
- No dollar figure: no `usd` or cost field in `codex:protocol` or `codex:app-server-protocol` (searched). Confirmed by absence; the only USD-shaped strings are in the backend's spend-control models (`SpendControlLimitSnapshot.limit` / `used`, strings with no unit).
- Subagents: a delegated sub-thread's `TokenCount` events are dropped, not forwarded to the parent (`codex:core/src/codex_delegate.rs:362-368`). Each thread keeps its own `TokenUsageInfo`. A guardian (auto-approval reviewer) rollout on this Mac has `source: {"subagent": {"other": "guardian"}}` and its own `token_count` (observed). Whether any parent total includes spawned agents: unconfirmed, no rollup found.

### Subscription versus API

- On ChatGPT plans, Codex use is limited per 5-hour window and per week ("Pro plans currently have no five-hour limit"; "Weekly limits may also apply"); past those, credits are spent, "metered by tokens, not by message", at credits per million input, cached input and output tokens. With an API key, Codex is billed "based on API pricing". Users see remaining limits with `/status` or the usage dashboard. Confirmed: [learn.chatgpt.com/docs/pricing](https://learn.chatgpt.com/docs/pricing).
- So Codex never reports dollars. On a plan, the rollout's `rate_limits` is the meaningful figure (percent of each window, plus the credit balance). On an API key, Admiral would have to price tokens itself with a table it keeps.

### Getting it into Admiral

The `PostModelRequest` hook is the cleanest: Admiral already installs Codex hooks, it fires per request with `agent_id`, and it needs no file parsing. Adding a hook event brings back Codex's hook review dialog once per fleet (ADR 0013). The quota lives only in the rollout's `token_count` and on the app-server, so Admiral reads the last `token_count` from the rollout (path from any hook's `transcript_path`) for the quota bar.

## Copilot CLI

### What it reports and where

| Source | Tokens | Dollars / credits | Premium requests | Quota | When | Subagents |
| --- | --- | --- | --- | --- | --- | --- |
| `events.jsonl` `session.usage_record` | `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, `reasoningTokens` | `copilotUsage.totalNanoAiu` and per-class `tokenDetails` with `costPerBatch` | `cost` (the model's multiplier) | No | Per model call, persisted | Yes: `initiator` `sub-agent`, `interactionType` `conversation-subagent` |
| `events.jsonl` `session.usage_checkpoint` | No | `totalNanoAiu` | `totalPremiumRequests` | No | Periodically (unconfirmed when) | Included |
| `events.jsonl` `session.shutdown` | Per model and per agent (`modelMetrics`, `agentMetrics`) | `totalNanoAiu` | `totalPremiumRequestCost`, `totalUserRequests` | No | At exit | Included, split by agent |
| SDK `assistant.usage` event | Same classes | No | `cost` multiplier | No | Live, ephemeral (not in the file) | Yes |
| SDK `session.usage.getMetrics` | Per model | `totalNanoAiu` | `totalPremiumRequestCost` | No | On demand, experimental | Yes |
| SDK `account.getQuota` | No | No | `premium_interactions` used / entitlement | `remainingPercentage`, `resetDate` | On demand | n/a |
| Hooks | No | No | No | No | n/a | n/a |

Sources:

- `session.usage_record`, `session.usage_checkpoint` and the `session.shutdown` `accountingSnapshot`: observed in this Mac's `events.jsonl` (200 recent sessions, 198 with usage records, all with a shutdown). `session.usage_record` and `session.usage_checkpoint` are not in the SDK's event table, so they are undocumented internals. `session.shutdown` with `modelMetrics` is documented ([docs/features/streaming-events.md](https://github.com/github/copilot-sdk/blob/8aed71413479f146592971bb909785ddbab13ac2/docs/features/streaming-events.md), line 1059).
- `assistant.usage` "is emitted once for every model API call in a turn (including calls made by sub-agents)", and "is ephemeral, so it is delivered live but not replayed". `session.usage.getMetrics` "aggregates every API call (main agent and sub-agents)". Cost is in nano-AI units, and the SDK says to treat GitHub's billing docs as the source of truth for the conversion. `account.getQuota` returns `quotaSnapshots` keyed by `premium_interactions`, `chat`, `completions`. Confirmed: [docs/features/usage-and-billing.md](https://github.com/github/copilot-sdk/blob/8aed71413479f146592971bb909785ddbab13ac2/docs/features/usage-and-billing.md), lines 19-61, 665-711, 1174-1183. `getMetrics` and `session.quota` are marked experimental.
- Hooks carry no usage: confirmed, [hooks-configuration](https://docs.github.com/en/copilot/reference/hooks-configuration) (`sessionEnd`: `sessionId`, `timestamp`, `cwd`, `reason`; `agentStop`: adds `transcriptPath`, `stopReason`).
- `--max-ai-credits <credits>`: "Set max AI credits for this session" (`copilot --help`). The SDK's equivalent is a soft cap: "Usage is checked after model calls return, so one response can exceed the configured value" ([docs/features/session-limits.md](https://github.com/github/copilot-sdk/blob/8aed71413479f146592971bb909785ddbab13ac2/docs/features/session-limits.md)).
- `--output-format json` exists for `-p` runs (`copilot --help`). Whether its stream carries `assistant.usage`: unconfirmed.

### Subscription versus API

- Copilot moved to AI credits on 2026-06-01: "1 AI credit = $0.01 USD"; cost "depends on the model used and the number of input, output, and cached tokens"; Copilot CLI is among the features that use credits. Plans carry a monthly allowance (Pro 1,500, Pro+ 7,000, Max 20,000) that resets on the 1st, UTC. Confirmed: [individuals/billing](https://docs.github.com/en/copilot/concepts/billing-and-usage/individuals/billing). The June date is from the [legacy requests page](https://docs.github.com/en/copilot/reference/copilot-billing/request-based-billing-legacy/copilot-requests), which says legacy premium-request billing remains only for Pro and Pro+ annual plans that stayed on it.
- Legacy: "Each prompt to Copilot CLI uses one premium request with the default model", multiplied by the model's rate; "only the prompts you send count", tool calls don't. Confirmed: same legacy page. This matches the files: every `usage_record` carries the multiplier (15 for Claude Opus 5.5, 1 for others) whoever initiated it, while one session's `totalPremiumRequestCost` was 15 for 3 calls and 1 user request (observed).
- So `totalNanoAiu / 1e9` is AI credits and `/ 1e11` is dollars. The 1e9 divisor is the SDK's own example, which it says to "confirm … matches current billing". Example from this Mac: 22,932,100,000 nano-AIU is 22.93 credits, about $0.23, for one Opus prompt. Unconfirmed against a GitHub bill.
- Copilot is the only harness whose number is the vendor's own charge rather than a client estimate, though a plan's included allowance is still "free" until it runs out. Per-account quota is not in the session file; it needs the SDK's `account.getQuota` (a separate headless runtime) or GitHub's usage dashboard.

### Getting it into Admiral

Tail `events.jsonl` (path fixed by `--session-id`) for `session.usage_record`, or read `session.shutdown` at the end. Both are internal or unstable formats; the documented live event, `assistant.usage`, is ephemeral and reaches only an SDK client, which the interactive TUI is not.

## Subagents and crew boats

- Claude Code: crew boats (subagents) write their own transcripts, already recorded in `@admiral-crew-transcript` (ADR 0021). Session totals (status line, `cost-state`, `-p` JSON, OTel) include them. Agent-team teammates are separate Claude processes with their own sessions and totals ([costs#agent-team-token-costs](https://code.claude.com/docs/en/costs)).
- Codex: each subagent and the guardian reviewer is its own thread; nothing rolls their tokens into the parent. `PostModelRequest`'s `agent_id` lets Admiral sum them.
- Copilot: subagent calls land in the same `events.jsonl` and the same totals, split by agent in `session.shutdown.agentMetrics`.

## What "cost" should mean in Admiral

- **Tokens** are the one figure all three report, per request, with cache reads and writes split. Admiral can always show them.
- **Dollars** have a different basis per harness: Claude's list-price estimate (an "API equivalent" on a subscription, overridable by a managed `modelPricing` table), Copilot's AI credits (a real charge once past the allowance), and nothing from Codex. A cross-harness dollar figure means Admiral keeps its own price table for Codex, or shows "n/a".
- **Quota** is what limits a subscriber: Claude's 5-hour and 7-day percentages, Codex's primary and secondary windows and credit balance, Copilot's monthly allowance. It is per account, so concurrent ships move the same bar and a per-ship share can only be estimated from token deltas.
- For the worker spend cap in [worker-guardrails.md](worker-guardrails.md) ($0.25 per turn, $5–10 per day): Claude Code's `--max-budget-usd` works only with `--print` (`claude --help`), so a TUI ship can't be capped by flag; Copilot's `--max-ai-credits` can cap a session (soft); Codex has no budget flag. Admiral would enforce the cap itself from the hook or file totals, which lag by one model request.

## Not verified

- Whether Claude's transcript `usage.output_tokens` is final or a placeholder (it looks final).
- Whether the Claude status line's `cost.total_cost_usd` includes subagents (the SDK's equal total does).
- `PostModelRequest` firing live on 0.166.0, and whether it fires for the guardian.
- When Copilot writes `session.usage_checkpoint`, and what `--output-format json` carries.
- The nano-AIU to dollar conversion against an actual GitHub invoice.
- OpenTelemetry from Claude Code (not probed).
