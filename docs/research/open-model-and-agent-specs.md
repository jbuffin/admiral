# Open model and agent specs: what each covers, who serves it, who speaks it

Research for [#974](https://github.com/jbuffin/admiral/issues/974), under the map [#971](https://github.com/jbuffin/admiral/issues/971). This note lays out the facts. It decides nothing.

There are two kinds of spec here, and they sit on different sides of the harness:

- **Model APIs** (Chat Completions, Open Responses, Anthropic Messages) sit between a harness and a **model server**. Under #971's standing rules only the harness speaks them. Admiral never does.
- **ACP** sits between an editor or other client and a harness. It is the only one Admiral could speak itself, and [ADR 0030](../adr/0030-ship-conversations-come-from-a-first-party-adapter-per-harness.md) allows that only as a second client beside the harness's own TUI.

Researched 2026-11-27 on this Mac: Copilot CLI 1.0.117, codex-cli 0.171.0, Claude Code 2.1.332, Ollama 0.40.17. Gemini CLI, `claude-agent-acp`, `codex-acp`, LM Studio, vLLM and llama.cpp are not installed, so what this note says about them comes from their source and docs. "Observed" means a command was run here. Nothing was installed and no config was changed. The only probe that started a harness sent ACP `initialize` to `copilot --acp` and created no session.

## Answer in one table

| | Chat Completions | Open Responses | Anthropic Messages | ACP |
| --- | --- | --- | --- | --- |
| **Sits between** | harness and model server | harness and model server | harness and model server | client (editor, dashboard) and harness |
| **Governed by** | OpenAI alone. There is no open spec: it is a de facto standard | A Linux Foundation project (LF Projects, Agentic AI Foundation), with a TSC from OpenAI, Databricks, Amazon, Hugging Face, Ollama, OpenRouter and Ramp. Apache-2.0 | Anthropic alone. Servers copy a subset of it | Zed and JetBrains jointly, for now ("interim governance"). Apache-2.0 |
| **Tool calls** | yes (`tools`, `tool_calls`) | yes: function items, plus hosted tools and `allowed_tools` | yes (`tool_use` / `tool_result`) | the agent reports tool calls and asks the client for permission |
| **Streaming** | SSE deltas | semantic SSE events per item; WebSocket transport since 2026-04-24 | SSE content-block events | `session/update` notifications |
| **Reasoning** | no field in OpenAI's spec; servers add `reasoning_content` or `reasoning` | `reasoning` item with `content`, `encrypted_content`, `summary` | `thinking` blocks | agent thought chunks in `session/update` |
| **Images** | `image_url` parts (URL or base64) | text and images, base64 or URL | base64 or URL image blocks | `promptCapabilities.image` |
| **Ollama** | yes | yes, stateless only | yes, a subset | n/a |
| **LM Studio** | yes | yes, including `previous_response_id` | yes | n/a |
| **vLLM** | yes | yes | yes, with `count_tokens` | n/a |
| **llama.cpp server** | yes | yes | yes ("no strong claims of compatibility") | n/a |
| **OpenRouter** | yes | yes | yes (`/messages`) | n/a |
| **Harnesses that speak it** | Copilot CLI (default wire API). Codex no longer does | Codex (its only wire API); Copilot with `COPILOT_PROVIDER_WIRE_API=responses` | Claude Code (its only format besides Bedrock and Vertex); Copilot with type `anthropic` | Copilot (`--acp`), Gemini CLI (`--acp`), Claude Code and Codex only through adapters |
| **Beside a running TUI?** | n/a | n/a | n/a | **No, for every harness checked.** Each ACP route is its own headless process |

## The model APIs

### OpenAI Chat Completions

- **Governance.** OpenAI owns it. It is published as OpenAI's API reference and OpenAPI document, with no outside process ([API reference](https://platform.openai.com/docs/api-reference/chat)). Every server here copies it, each with its own extensions.
- **Coverage.** Messages, `tools` with `tool_calls`, SSE streaming and `image_url` content parts. Reasoning is not standard. llama.cpp puts thoughts in `message.reasoning_content` under `--reasoning-format deepseek` ([llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)), OpenRouter uses `reasoning` and `reasoning_details` ([OpenRouter API overview](https://openrouter.ai/docs/api/reference/overview)), and Ollama has its own thinking control ([Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)).
- **Harnesses.** Copilot CLI's BYOK default: `COPILOT_PROVIDER_WIRE_API` is `"completions"` (default) or `"responses"`, and "Use 'openai' type for any OpenAI-compatible endpoint, including Ollama, vLLM, and Foundry Local" (observed, `copilot help providers-env`). **Codex dropped it.** `WireApi` has the single variant `Responses`, and `wire_api = "chat"` fails with "`wire_api = "chat"` is no longer supported" ([codex-rs/model-provider-info/src/lib.rs](https://github.com/openai/codex/blob/main/codex-rs/model-provider-info/src/lib.rs), on `main`; that 0.171.0 matches is assumed, not checked).

### Open Responses

- **Governance.** A series of LF Projects. The [charter](https://github.com/openresponses/openresponses/blob/main/CHARTER.md) names the Agentic AI Foundation, a directed fund of the Linux Foundation, as its contact. The TSC's first voting members are its [maintainers](https://github.com/openresponses/openresponses/blob/main/MAINTAINERS.md): two from OpenAI, and one each from Databricks, Amazon, Ollama, OpenRouter and Ramp, plus two from Hugging Face. Apache-2.0. Repo: [openresponses/openresponses](https://github.com/openresponses/openresponses).
- **What it is.** "Based on the OpenAI Responses API" and provider-neutral. The current spec is dated 2026-04-24 (the previous one 2026-01-15), and that version added a WebSocket transport ([specification](https://www.openresponses.org/specification)).
- **Coverage** ([specification](https://www.openresponses.org/specification)):
  - **Items** are the unit of context, typed by `type` (`message`, `function_call`, `function_call_output`, `reasoning`), each with status `in_progress`, `incomplete` or `completed`.
  - **Streaming** is semantic events: `response.output_item.added` / `.done`, `response.content_part.*`, `response.<type>.delta`. A stream "MUST include a terminal response event".
  - **Reasoning** items carry optional `content` (raw), `encrypted_content` (opaque) and `summary`.
  - **Images:** user content holds text and images, base64 or URL.
  - **Tools:** external tools (functions, MCP) and provider-hosted tools; `tool_choice` and an enforced `allowed_tools`.
  - **State:** `previous_response_id` makes the server load the prior input and output.
  - **Extensions** must carry an implementer prefix (`acme:…`), and clients ignore ones they don't know.
  - There is an acceptance-test page at `/compliance`. Its contents were not read.
- **Who serves it.** The site lists Ollama, LM Studio, vLLM, OpenRouter, Hugging Face, NVIDIA, Vercel, Databricks, Red Hat, AWS, OpenAI and Llama Stack as backers ([openresponses.org](https://www.openresponses.org/)). Being a backer is not a conformance claim. What each server's own docs say:
  - **Ollama** serves `/v1/responses` from v0.13.3, with streaming, tools and reasoning summaries, but "only the non-stateful flavor": no `previous_response_id`, `conversation` or `truncation` ([Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)).
  - **LM Studio** serves `/v1/responses` with `previous_response_id`, streaming, `reasoning.effort` and opt-in remote MCP tools ([LM Studio Responses](https://lmstudio.ai/docs/developer/openai-compat/responses)).
  - **vLLM** serves `/v1/responses`, `/v1/responses/{id}` and `.../cancel` ([vLLM online serving](https://docs.vllm.ai/en/latest/serving/online_serving/)).
  - **llama.cpp** serves `POST /v1/responses`. Tools go through the chat template, and the client returns `function_call_output` items ([server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)).
  - **OpenRouter** serves `/responses` and has deprecated its `beta.responses` alias ([OpenAPI](https://openrouter.ai/openapi.json), [llms.txt](https://openrouter.ai/docs/llms.txt)).
  - None of them says "conforms to Open Responses" in the pages read. They say "OpenAI-compatible Responses".
- **Harnesses.** Codex speaks only Responses. Its `--oss` / `--local-provider` picks `ollama` or `lmstudio` (observed in `codex --help`; [Codex advanced config](https://learn.chatgpt.com/docs/config-file/config-advanced)). Copilot speaks it with `COPILOT_PROVIDER_WIRE_API=responses`, recommended "for GPT-5 series models" (observed).
- **Caveat for Codex on Ollama (unverified):** Ollama's Responses is stateless. Whether Codex relies on `previous_response_id` against a custom provider was not checked.

### Anthropic Messages, as Ollama and others serve it

- **Governance.** Anthropic's API, which servers copy. Ollama's docs set the expectation that it covers part of the API, not all of it.
- **Ollama's coverage** ([Ollama Anthropic compatibility](https://docs.ollama.com/api/anthropic-compatibility)):
  - **Yes:** messages, streaming events (`message_start` … `message_stop`), system prompts, function tools and tool results, `defer_loading`, thinking (with `budget_tokens` accepted but not enforced), base64 images, `output_config.effort`.
  - **No:** `count_tokens`, `cache_control`, batches, `tool_choice`, `metadata`, citations, PDF `document` blocks, URL images. Errors mid-stream come back as an HTTP status, not an `error` event.
  - **For Claude Code:** `ANTHROPIC_BASE_URL=http://localhost:11434` and `ANTHROPIC_AUTH_TOKEN=ollama`.
  - The Ollama version that added this endpoint is not on the page (unverified).
- **Others serving it.**
  - **LM Studio:** `/v1/messages` with streaming and tools; thinking and images aren't documented ([LM Studio Messages](https://lmstudio.ai/docs/developer/anthropic-compat/messages)).
  - **vLLM:** `/v1/messages` and `/v1/messages/count_tokens` ([vLLM online serving](https://docs.vllm.ai/en/latest/serving/online_serving/)).
  - **llama.cpp:** `POST /v1/messages` with SSE streaming, though "no strong claims of compatibility with the Anthropic API spec are made" ([server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)).
  - **OpenRouter:** `/messages` ([OpenAPI](https://openrouter.ai/openapi.json)).
- **Harnesses.**
  - **Claude Code** speaks only Anthropic Messages (`ANTHROPIC_BASE_URL`, `/v1/messages`, with `count_tokens` optional), Bedrock InvokeModel and Vertex rawPredict ([gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol.md)). It has no OpenAI wire format. Anthropic "doesn't support routing Claude Code to non-Claude models through any gateway" ([LLM gateways](https://code.claude.com/docs/en/llm-gateway)): it works, and Anthropic does not stand behind it. Without `count_tokens`, Claude Code estimates context usage from characters.
  - **Copilot** accepts `COPILOT_PROVIDER_TYPE=anthropic` (observed).

### How each harness reaches Ollama today

| Harness | Route | Source |
| --- | --- | --- |
| Claude Code | `ANTHROPIC_BASE_URL` to Ollama's Anthropic Messages endpoint | Ollama docs |
| Codex | `--oss --local-provider ollama`, or a `[model_providers.*]` entry with `wire_api = "responses"` | `codex --help`, Codex docs |
| Copilot | Finds Ollama at `127.0.0.1:11434` (or `OLLAMA_HOST`) on its own and lists it under "Local and self-hosted" in `/model`; or `COPILOT_PROVIDER_*` env; or `providers.json` (`COPILOT_PROVIDERS_CONFIG`) | observed, `copilot help providers-local` |
| all three | `ollama launch claude|codex|copilot` configures and starts the harness | observed, `ollama launch --help` |

Copilot's own help notes two things that matter for #971's rule that a harness on a model server must not change its other sessions. Models added in `/model` are saved to `providers.json` in the Copilot home, which every session reads. `COPILOT_PROVIDER_*` variables apply only to the process that has them, and a `providers.json` named by `COPILOT_PROVIDERS_CONFIG` wins over them. `COPILOT_OFFLINE=true` with a local provider turns off all network access, including GitHub sign-in and the GitHub MCP server (observed).

## Agent Client Protocol (ACP)

### What it is and who governs it

- **Governance.** "ACP is jointly governed by Zed and JetBrains … while working toward transitioning to an independent foundation" ([GOVERNANCE](https://github.com/agentclientprotocol/agent-client-protocol/blob/main/docs/community/governance.mdx)). Apache-2.0. Repo: [agentclientprotocol/agent-client-protocol](https://github.com/agentclientprotocol/agent-client-protocol). Releases on 2026-11-23: `v1.22.0`, `schema-v1.35.0` and `schema-v2.0.0-beta.4`, so protocol v2 is in beta.
- **Transport.** "Local agents run as sub-processes of the code editor, communicating via JSON-RPC over stdio." Remote over HTTP or WebSocket is "still in progress" ([introduction](https://agentclientprotocol.com/overview/introduction); [Streamable HTTP & WebSocket RFD](https://github.com/agentclientprotocol/agent-client-protocol/blob/main/docs/rfds/streamable-http-websocket-transport.mdx)).
- **Coverage.** Sessions (`session/new`, `session/load`, `session/list`, `session/close`, with `session/resume`, `session/status`, fork and others as RFDs), prompts, streamed `session/update` notifications (text, thoughts, tool calls, plans), permission requests from agent to client, client-side file and terminal methods, MCP passthrough, elicitation, and auth methods.
- **Model servers through ACP.** The [Configurable LLM Providers RFD](https://github.com/agentclientprotocol/agent-client-protocol/blob/main/docs/rfds/custom-llm-endpoint.mdx) proposes `providers/list`, `providers/set` and `providers/disable` so a client can point an agent at "local servers (vLLM, Ollama, etc.)". It is not in the stable spec. `codex-acp` offers a client-provided OpenAI-compatible gateway as an auth method ([README](https://github.com/agentclientprotocol/codex-acp)).

### Can an ACP client attach beside a running TUI?

**No, for every harness checked. ACP as specified and shipped means a separate session process that the client starts and owns, with no TUI.**

1. **The spec is one client per connection.** The open [Multi-Client Session Attach RFD](https://github.com/agentclientprotocol/agent-client-protocol/pull/533) (status "proposal", open since 2026-02-18, last updated 2026-11-18, not merged) states the status quo: "ACP currently assumes a **1:1 relationship** between a client and an agent session." It adds: "`session/load` allows a new client to resume a previous session, but the original client must have disconnected first. There is no mechanism for a second client to 'attach' to a live, in-progress session." Its proposed `session/attach` would treat every attached client as an equal that can prompt and answer permissions (first answer wins), so it is not a read-only observer either. The [Session status RFD](https://github.com/agentclientprotocol/agent-client-protocol/blob/main/docs/rfds/session-status.mdx) defers "multi-client attach semantics" to that PR.
2. **Copilot CLI 1.0.117.** `--acp` "Start[s] as Agent Client Protocol server" (observed, `copilot --help`). It is a mode of its own, with no TUI: over stdio by default, or over TCP with `--acp --port N`, which binds 127.0.0.1 ([GitHub docs: ACP server](https://docs.github.com/en/copilot/reference/acp-server)). Observed `initialize` reply: `protocolVersion: 1`, `loadSession: true`, `sessionCapabilities: {close, list}`, image prompts, no `resume` and no `status`. `copilot help acp` says that "loading a session that another connection still has open keeps that connection's classification", so one ACP server process can serve one session to several ACP connections. The TUI is not one of those connections. **Unverified:** what a separate `copilot --acp` does when asked to `session/load` a session id that a live TUI holds. Either way it would be a second process driving the session, not a reader beside it. Admiral's beside-the-TUI route for Copilot remains the non-ACP `--ui-server` of ADR 0030.
3. **Gemini CLI v0.71.1** (not installed; read from source). `--acp` "Starts the agent in ACP mode", and `--experimental-acp` is a deprecated alias ([config.ts](https://github.com/google-gemini/gemini-cli/blob/v0.71.1/packages/cli/src/config/config.ts)). In [gemini.tsx](https://github.com/google-gemini/gemini-cli/blob/v0.71.1/packages/cli/src/gemini.tsx), `if (config.getAcpMode()) return runAcpClient(...)` runs before the interactive UI starts, so ACP mode never draws the TUI. ACP mode also drops the `ask_user` tool.
4. **Claude Code through `claude-agent-acp`** (v0.113.0, 2026-11-26; it moved from Zed's org to [agentclientprotocol/claude-agent-acp](https://github.com/agentclientprotocol/claude-agent-acp)). It "implements an ACP agent by using the official Claude Agent SDK". Each release bundles the `claude` CLI it runs, or uses `CLAUDE_CODE_EXECUTABLE`. That is a headless SDK session, the route ADR 0030 already rejects for Claude Code.
5. **Codex through `codex-acp`.** Zed's repo now says "Development has moved to agentclientprotocol/codex-acp" ([zed-industries/codex-acp](https://github.com/zed-industries/codex-acp), last release v0.16.0). The new one (v2.12.1, 2026-11-26) "is a stdio ACP agent server. It starts the Codex App Server" ([README](https://github.com/agentclientprotocol/codex-acp)). Its `CodexAppServerSupervisor` "owns the lifecycle of the Codex app-server child", started with `app-server` arguments ([source](https://github.com/agentclientprotocol/codex-acp/blob/main/src/app-server-recovery/CodexAppServerSupervisor.ts)). No option to connect to an app-server that is already running was found (searched by hand, not exhaustively). So it cannot join the per-ship app-server that Admiral's Codex wrapper already starts with the TUI on `--remote` (ADR 0030). The beside-the-TUI route there is Codex's own app-server protocol, not ACP.

**What follows for #971's open question** (facts, not a decision): a harness whose only structured route is ACP gives Admiral a conversation only by running ACP as the ship. That reverses ADR 0030, and #971 rules it out of scope. Such a harness falls back to its transcript file, if it writes one, and the Terminal tab. ACP could read beside a TUI only if the harness's own TUI were an ACP client of a shared agent process (the way Codex's TUI is a client of its app-server with `--remote`) and the agent accepted a second connection on the same session. None of the harnesses checked works that way today. The Multi-Client Attach RFD, if it lands, defines the second-connection half, and only for agents or proxies that host the session.

## Not verified

- Whether a separate `copilot --acp` can `session/load` a session that a live TUI holds open, and what that does to the TUI's session (not run: it would touch real session state).
- That Codex 0.171.0's `WireApi` matches `main` (read on `main`), and whether Codex needs `previous_response_id`, which Ollama doesn't serve.
- The Ollama version that added `/v1/messages`.
- LM Studio's `/v1/messages` support for thinking and images.
- vLLM's tool-calling and reasoning flags (`--enable-auto-tool-choice`, `--tool-call-parser`, reasoning parsers) were not read on its OpenAI-compatible server page.
- Open Responses' acceptance tests, and which servers pass them. No server read claims conformance by name.
- Gemini CLI, `claude-agent-acp`, `codex-acp`, LM Studio, vLLM and llama.cpp were read from source and docs only. None is installed here.
