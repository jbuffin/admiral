# How each of the big three runs on a model server

Research for [#972](https://github.com/jbuffin/admiral/issues/972), under the map [#971](https://github.com/jbuffin/admiral/issues/971). This note gathers facts; it does not pick a design.

Question: can Claude Code, Codex CLI and Copilot CLI each run their normal terminal UI on a model server (Ollama first; LM Studio, vLLM, OpenRouter after)? How is each one set up, which open API does it speak to the server, and what does `ollama launch` do for it? Can one ship run on Ollama while another in the same fleet and config directory runs on the vendor, with nothing leaking between them? Which features break on a local model?

Versions on this Mac on 2026-11-27: Claude Code 2.1.332, codex-cli 0.171.0, GitHub Copilot CLI 1.0.117, Ollama 0.40.17 (already serving on `127.0.0.1:11434`, models `qwen3:4b-instruct` and `llama3.2:3b`, loaded at a 32768-token context).

"Observed" means a probe ran here. Every probe used a scratch config home (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`) and `env -i`, the TUI probes ran on a private tmux socket, and every call went to the local Ollama with `qwen3:4b-instruct`. Nothing touched the engineer's config, login or tmux, and no call went to a paid API. The rest comes from vendor docs, `--help` output, or source code at the installed version: Ollama at tag `v0.40.17` and Codex at tag `rust-v0.171.0`.

## Summary

| | Claude Code | Codex CLI | Copilot CLI |
| --- | --- | --- | --- |
| **Runs its normal TUI on Ollama** | Yes (observed in tmux) | Yes (observed in tmux) | Yes (observed in tmux) |
| **API it speaks to the server** | Anthropic Messages (`/v1/messages`) | OpenAI Responses only (`/v1/responses`); `wire_api = "chat"` was removed | OpenAI Chat Completions (default) or Responses; also `anthropic`, `azure`, `google` types |
| **How it is set up** | Env vars only: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY=""`, the `ANTHROPIC_DEFAULT_*_MODEL` family, `CLAUDE_CODE_SUBAGENT_MODEL` | `--oss --local-provider ollama`, or `-c model_provider="ollama"`, or a custom `[model_providers.X]` passed with `-c` or a profile file | Env vars only: `COPILOT_PROVIDER_BASE_URL`, `COPILOT_PROVIDER_WIRE_API`, `COPILOT_MODEL` (or a saved `providers.json`) |
| **Files it writes for this** | None | None with `--oss` or `-c` (observed: no `config.toml` created) | None with env vars; `/model` saves to `$COPILOT_HOME/providers.json` |
| **Vendor login needed** | No; the auth token replaces the claude.ai login for those requests | No; the OSS provider skips the login | No; GitHub auth "is not required" |
| **What `ollama launch` does** | Runs `claude --model M` with about 25 env vars set; writes nothing | Writes `~/.codex/ollama-launch.config.toml` and `~/.codex/model.json`, then runs `codex --profile ollama-launch` with `-c` overrides | Runs `copilot --model M` with 4 env vars set; writes nothing |
| **`--session-id` and resume** | Both work (observed) | Resume by id works (observed); the rollout records `model_provider: "ollama"` | Both work (observed); `events.jsonl` records `provider: "openai-responses"` |
| **Isolation per ship** | Possible, with care: settings-file `env` overrides the shell unless `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1` | Clean when provider config goes on the command line | Clean: provider env vars win over `providers.json` |

The main risks are the same for all three. The context window the harness assumes doesn't match what Ollama loaded. The harness's own prompt is big (about 12k–28k input tokens on the first turn here). Usage and cost figures are wrong or missing. Subagents and background calls must also be pointed at the local model.

## Claude Code

### Setup and API

Claude Code speaks only the Anthropic Messages format to a custom endpoint. The gateway guide lists the endpoints it calls: `/v1/messages` and, optionally, `/v1/messages/count_tokens` ([gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol)). Ollama serves `/v1/messages` on its normal port ([Ollama Anthropic compatibility](https://github.com/ollama/ollama/blob/v0.40.17/docs/api/anthropic-compatibility.mdx)).

Ollama's manual setup is three env vars, then `claude --model <m>` ([Ollama: Claude Code](https://github.com/ollama/ollama/blob/v0.40.17/docs/integrations/claude-code.mdx)):

```shell
ANTHROPIC_AUTH_TOKEN=ollama ANTHROPIC_API_KEY="" ANTHROPIC_BASE_URL=http://localhost:11434 claude --model qwen3.5
```

What the variables do, from the [env-vars reference](https://code.claude.com/docs/en/env-vars):

- `ANTHROPIC_BASE_URL` overrides the API endpoint. Remote Control is disabled when it points anywhere but `api.anthropic.com`.
- `ANTHROPIC_AUTH_TOKEN` sets the `Authorization: Bearer` value.
- `ANTHROPIC_API_KEY`, when set to a non-empty value, replaces the subscription. Interactive mode asks once before using it. Setting it to an empty string avoids that.
- `ANTHROPIC_DEFAULT_OPUS_MODEL`, `_SONNET_MODEL` and `_HAIKU_MODEL` map the aliases. The Haiku one is also "used for background functionality".
- `CLAUDE_CODE_SUBAGENT_MODEL` is the default model for subagents, agent-team teammates and workflow agents.

**Login.** "While a gateway credential variable or `apiKeyHelper` is active, requests carry that credential in place of a developer's claude.ai subscription login … Claude Code keeps a saved claude.ai login on the machine but doesn't send it with those requests." Setting only `ANTHROPIC_BASE_URL`, with no credential, leaves the saved login active and sends it to the endpoint ([LLM gateways](https://code.claude.com/docs/en/llm-gateway)). So a ship on a model server must always set `ANTHROPIC_AUTH_TOKEN`, or the engineer's claude.ai OAuth token goes to Ollama.

**Anthropic's stance.** Anthropic "doesn't support routing Claude Code to non-Claude models through any gateway" ([LLM gateways](https://code.claude.com/docs/en/llm-gateway)). It works, but it is unsupported.

### What `ollama launch claude` does

From [`cmd/launch/claude.go`](https://github.com/ollama/ollama/blob/v0.40.17/cmd/launch/claude.go): it finds `claude` (offering to install it if missing), then runs it as a child process (`exec.Command`, not `exec`), with `--model M` plus any args after `--`. It appends these to the inherited environment and writes no Claude file:

- Routing: `ANTHROPIC_BASE_URL=<OLLAMA_HOST>`, `ANTHROPIC_API_KEY=`, `ANTHROPIC_AUTH_TOKEN=ollama`, and `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1` ("Keep provider routing scoped to this launch, including when Claude settings contain a different endpoint or credentials").
- It clears every other provider switch: `CLAUDE_CODE_USE_BEDROCK`, `_VERTEX`, `_FOUNDRY`, `_ANTHROPIC_AWS`, `_ANTHROPIC_GOOGLE_CLOUD`, `_MANTLE` and `_GATEWAY`, all set to empty.
- Turned off: `ENABLE_CLAUDEAI_MCP_SERVERS=false`, `CLAUDE_CODE_ATTRIBUTION_HEADER=0`, `CLAUDE_CODE_TOTAL_TOKENS_REMINDER=off`, `DISABLE_ERROR_REPORTING=1`, `DISABLE_FEEDBACK_COMMAND=1`, `CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1`.
- `CLAUDE_CODE_AUTO_MODE_SERVER=0` unless already set ("Ollama does not provide Anthropic's server-side auto-mode checks"), so auto mode uses Claude Code's own classifier.
- Every tier goes to one model: `ANTHROPIC_DEFAULT_OPUS_MODEL`, `_SONNET_MODEL` and `_HAIKU_MODEL`, plus `CLAUDE_CODE_SUBAGENT_MODEL`, all set to M.
- **Only for cloud or remote models**, it sets `CLAUDE_CODE_MAX_OUTPUT_TOKENS`, `CLAUDE_CODE_MAX_CONTEXT_TOKENS` and `CLAUDE_CODE_AUTO_COMPACT_WINDOW` from the model's metadata. For a local model it sets none of them.

Separately, `ollama launch` records the last model per integration in `~/.ollama/config.json` ([`cmd/config/config.go`](https://github.com/ollama/ollama/blob/v0.40.17/cmd/config/config.go)).

`CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` is documented. It is "set by host platforms that embed Claude Code and manage model provider routing on its behalf. When set, Claude Code ignores provider-selection, endpoint, and authentication variables such as `CLAUDE_CODE_USE_BEDROCK`, `ANTHROPIC_BASE_URL`, and `ANTHROPIC_API_KEY` in settings files, so user settings can't override the host's routing." It also ignores model-selection keys in managed settings ([env-vars](https://code.claude.com/docs/en/env-vars)).

### Observed

All probes used a scratch `CLAUDE_CONFIG_DIR`, the env vars above, and `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1`.

- **`-p` with a tool call.** `claude -p --session-id <uuid> --output-format json --allowedTools Read`, asked to read a file. The model made a `Read` tool call (`num_turns: 2`) and named the wrong file, which is a 4B-model quality problem, not a protocol one. Claude Code printed `[claude-code:unrecognized_model]`.
- **Resume.** `--resume <uuid>` twice: the second run read the file and answered. The transcript is the usual `projects/<cwd>/<uuid>.jsonl`. Assistant lines carry `"model": "qwen3:4b-instruct"` and normal `tool_use` and `tool_result` blocks, so ADR 0030's adapter reads it unchanged.
- **Hooks.** `--settings` with `PreToolUse` and `Stop` command hooks. Both fired (`tool_name: "Read"`, `hook_event_name: "Stop"`), as expected, since hooks run on the client.
- **Subagent.** I asked the model to use the Agent tool. It read the file itself instead (`subagent_stats.spawned: 0`). A spawned subagent on a local model is **unverified**.
- **TUI.** `claude --model M --session-id …` in tmux opened the TUI. The scratch config dir showed first-run onboarding (the theme picker). That comes from the empty config dir: with the engineer's existing config dir the onboarding is already done, but that case is **unverified** with these env vars.
- **Usage.** `--output-format json` reported `total_cost_usd: 0.07129` for the first local call, with `costBasis: "unknown"` and `contextWindow: 1000000`. Both are made up: the call cost nothing, and Ollama had loaded the model at 32768 tokens. The first request was about 11.9k input tokens. Ollama does report `cache_read_input_tokens` on later turns (11780 here).

### What breaks or degrades

- **Context size.** Claude Code assumed a 1M window. Ollama ran this model at 32k, and `ollama launch` sets no context variable for local models. Auto-compaction then never triggers before the server's own limit. Fixes: set `CLAUDE_CODE_MAX_CONTEXT_TOKENS` (and `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, minimum 100k) to the server's real context, or raise Ollama's context. Ollama recommends 64k or more ([Ollama: Claude Code](https://github.com/ollama/ollama/blob/v0.40.17/docs/integrations/claude-code.mdx)). Ollama's own behavior past its window is **unverified** here.
- **Cost and plan usage.** The USD cost is invented, and plan-limit headers (`anthropic-ratelimit-unified-*`) never arrive. Token counts arrive.
- **Not supported by Ollama** ([compatibility table](https://github.com/ollama/ollama/blob/v0.40.17/docs/api/anthropic-compatibility.mdx)):
  - `/v1/messages/count_tokens`: Claude Code falls back to a character estimate, so `/context` is approximate.
  - `tool_choice` and `metadata`.
  - Prompt caching (`cache_control`).
  - PDF `document` blocks and citations.
  - URL images.
  - Ollama does accept `defer_loading` and `tool_reference`, so MCP tool search can work.
- **Off by design on a custom base URL:** Remote Control. Hosted WebSearch is "not fully supported" ([Ollama: Claude Code](https://github.com/ollama/ollama/blob/v0.40.17/docs/integrations/claude-code.mdx)). Fast mode's availability check goes to `api.anthropic.com` directly ([gateway guide](https://code.claude.com/docs/en/llm-gateway-protocol)).
- **Every model call goes to the one local model.** That covers background calls (titles, summaries: the Haiku alias), subagents (`CLAUDE_CODE_SUBAGENT_MODEL`), and the auto-mode classifier when `CLAUDE_CODE_AUTO_MODE_SERVER=0`. If any alias is left unmapped, those calls ask Ollama for a `claude-*` id it doesn't have. **Unverified** what Claude Code shows then.
- **Tool use depends on the model.** Ollama: "Use tools with compatible models".

## Codex CLI

### Setup and API

Codex 0.171.0 speaks **only** the Responses API to any provider. In [`model-provider-info/src/lib.rs`](https://github.com/openai/codex/blob/rust-v0.171.0/codex-rs/model-provider-info/src/lib.rs), `WireApi` has a single variant, `Responses`. A `wire_api = "chat"` entry fails to load with "`wire_api = "chat"` is no longer supported", and the old `ollama-chat` provider is removed too. So a model server must serve `/v1/responses`.

Codex has two built-in local providers, `ollama` (port 11434) and `lmstudio` (port 1234). Both have `requires_openai_auth: false`, no `env_key`, and `supports_websockets: false`. The experimental env vars `CODEX_OSS_BASE_URL` and `CODEX_OSS_PORT` override their address. There are three ways to use them, all on the command line (`codex --help`):

- `codex --oss --local-provider ollama -m <model>`. Without `-m`, the default is `gpt-oss:20b`. Before starting, `--oss` checks that Ollama is at least 0.13.4 for Responses, and **pulls the model if it is missing** ([`ollama/src/lib.rs`](https://github.com/openai/codex/blob/rust-v0.171.0/codex-rs/ollama/src/lib.rs)).
- `codex -c model_provider="ollama" -m <model>`. Observed working with `codex exec`. `codex app-server` takes `-c` but has no `--oss` flag, so this is the form that fits ADR 0030's per-ship app-server.
- A custom provider, for another host or port: `-c model_providers.X.base_url=…`, `-c model_providers.X.wire_api="responses"`, `-c model_provider="X"`. Or a profile file: `-p/--profile <name>` "Layer[s] `$CODEX_HOME/<name>.config.toml` on top of the base user config".

`set_default_oss_provider` (which writes `oss_provider` to `config.toml`) exists in core, but nothing outside its tests calls it at this tag. Observed: a `--oss` run made no `config.toml` in the scratch `CODEX_HOME`.

Ollama's Responses support is "the non-stateful flavor … no `previous_response_id` or `conversation` support", with streaming, function tools and reasoning summaries ([Ollama OpenAI compatibility](https://github.com/ollama/ollama/blob/v0.40.17/docs/api/openai-compatibility.mdx)). Codex worked against it here.

### What `ollama launch codex` does

From [`cmd/launch/codex.go`](https://github.com/ollama/ollama/blob/v0.40.17/cmd/launch/codex.go):

- It requires codex 0.134.0 or later.
- **It writes files into `~/.codex`, a fixed path that ignores `CODEX_HOME`:**
  - `ollama-launch.config.toml`: `model`, `model_provider = "ollama-launch"`, `model_catalog_json`, and `[model_providers.ollama-launch]` with `base_url = <host>/v1/` and `wire_api = "responses"`.
  - `model.json`: a model catalog with context window, vision and thinking settings for the installed models.
  - It also removes a legacy `profile = "ollama-launch"` and `[profiles.ollama-launch]` from `config.toml` if present, with a backup.
- Then it runs `codex --profile ollama-launch` with the provider and catalog repeated as `-c` overrides, plus `-c service_tier="default"`, `-m M`, and `OPENAI_API_KEY=ollama` in the env.
- It **refuses** extra args that set `-p`, `--profile`, `-m`, `--model`, `model_provider`, `model_catalog_json` or any `model_providers.*`.
- `--restore` removes the profile and the catalog.

The catalog fixes a warning observed without it: "Model metadata for `qwen3:4b-instruct` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."

### Observed

Scratch `CODEX_HOME`, `env -i`:

- **Tool call.** `codex exec --oss --local-provider ollama -m qwen3:4b-instruct -s read-only --json` ran `cat note.txt` through its shell tool and answered correctly. `turn.completed.usage` reported `input_tokens 27852`, `cached_input_tokens 13896` and `output_tokens 52`.
- **Resume.** `codex exec … resume <thread id>` recalled the earlier answer. The rollout's `session_meta` has `model_provider: "ollama"`.
- **Provider from `-c` alone.** `codex exec -c model_provider="ollama" -m …` worked with no `--oss`.
- **TUI.** `codex --oss --local-provider ollama -m M` in tmux ran a turn ("Worked for 18s"). The footer showed `qwen3:4b-instruct` and "2 warnings".

### What breaks or degrades

- **Model metadata.** Without a catalog entry, Codex uses fallback metadata (context window, tool and reasoning settings). Admiral would need its own `model_catalog_json`, or would rely on Codex's fallback.
- **Server-side features.** Stateful Responses, standalone web search and WebSockets are off for the OSS providers. Ollama runs Codex's web-search requests itself when signed in to ollama.com ([Ollama: Codex](https://github.com/ollama/ollama/blob/v0.40.17/docs/integrations/codex.mdx)). That is a network call from a "local" ship.
- **`--oss` may pull a model.** That is a multi-GB download inside a ship's pane, if the model is missing or `-m` is left off.
- **Hooks.** These are client-side and should be independent of the provider. **Unverified** on a local model, since the hook-trust dialog was not exercised.
- **App-server and `--remote`.** Not probed. The provider has to be set on `codex app-server` (with `-c`), since with `--remote` the agent runs in the server. **Unverified:** that the TUI's `-m` reaches a server started with `-c model_provider="ollama"`, and that the observing client gets the same events.

## Copilot CLI

### Setup and API

Copilot has a first-class bring-your-own-model route ([BYOK docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models); `copilot help providers`, `providers-local`, `providers-env`, `environment`):

- **Env vars (per process).**
  - `COPILOT_PROVIDER_BASE_URL`: "When set, the CLI uses this provider instead of GitHub Copilot's model routing and ignores a saved `providers.json`, unless COPILOT_PROVIDERS_CONFIG names a providers file, which then wins. GitHub authentication is not required."
  - `COPILOT_PROVIDER_TYPE`: `openai` (default; "for OpenAI-compatible endpoints including Ollama, vLLM, and Foundry Local"), `azure`, `anthropic`, or `google` (experimental).
  - `COPILOT_PROVIDER_WIRE_API`: `completions` (default) or `responses`. `COPILOT_PROVIDER_TRANSPORT=websockets` works only with openai plus responses.
  - `COPILOT_MODEL`, or `--model`. "A model is required for BYOK."
  - Optional: `COPILOT_PROVIDER_API_KEY`, `_BEARER_TOKEN`, `_API_KEY_COMMAND`, `_HEADERS`, `_MAX_PROMPT_TOKENS`, `_MAX_OUTPUT_TOKENS`, `_VISION`, `_MODEL_ID`, `_WIRE_MODEL`.
- **Saved providers.** `/model` finds Ollama at `127.0.0.1:11434` (or `OLLAMA_HOST`, if it is on this machine) under "Local and self-hosted". Adding a model saves it to `$COPILOT_HOME/providers.json`, or to `COPILOT_PROVIDERS_CONFIG`.
- **`COPILOT_OFFLINE=true`** with a local provider skips all network access: GitHub sign-in, web tools, the GitHub MCP server, telemetry and auto-update.

So Copilot speaks OpenAI Chat Completions or Responses, or Anthropic Messages with `TYPE=anthropic`, to the server.

### What `ollama launch copilot` does

From [`cmd/launch/copilot.go`](https://github.com/ollama/ollama/blob/v0.40.17/cmd/launch/copilot.go): it finds `copilot`, then runs it as a child with `--model M` plus extra args. It appends `COPILOT_PROVIDER_BASE_URL=<host>/v1`, `COPILOT_PROVIDER_API_KEY=`, `COPILOT_PROVIDER_WIRE_API=responses` and `COPILOT_MODEL=M`, and writes no files.

### Observed

Scratch `COPILOT_HOME`, `env -i`, Ollama over Responses:

- **`-p` with a tool call.** `copilot --session-id <uuid> --allow-tool read -p "…"` read the file ("● Read note.txt 1 line read") and answered. The summary showed tokens (`↑ 32.4k (16.2k cached) • ↓ 28`) and no premium-request count. No GitHub credentials were in the scratch home.
- **Resume.** `--resume=<uuid> -p …` recalled the answer. `session-state/<uuid>/events.jsonl` has the usual event types and `"provider": "openai-responses"`, `"model": "qwen3:4b-instruct"`, so ADR 0030's file fallback reads it. Nothing about the provider was written to `config.json`.
- **TUI.** `copilot --session-id … -i "…"` in tmux opened the TUI with two warnings. The first: "Model "qwen3:4b-instruct" is not in the built-in catalog. Using defaults for: prompt tokens (COPILOT_PROVIDER_MAX_PROMPT_TOKENS), output tokens …". The second: "Model catalog ownership cannot be verified for the active credentials". Then the usual folder-trust dialog. (The TUI showed v1.0.115 rather than 1.0.117 because `COPILOT_AUTO_UPDATE=false` runs the version built into the binary rather than the cached update.)

### What breaks or degrades

- **Token limits.** For a model not in Copilot's catalog, Copilot uses default token limits unless `COPILOT_PROVIDER_MAX_PROMPT_TOKENS` and `_MAX_OUTPUT_TOKENS` are set. The help says to set the model's `maxPromptTokens` to the server's context size.
- **Images.** BYOK models are text-only unless `COPILOT_PROVIDER_VISION=true`.
- **GitHub-hosted features.** "Auto model selection isn't available. Features that run on GitHub's servers use GitHub-hosted models … cloud agent, code review, remote sessions, and cloud sandboxes." "Copilot doesn't show Copilot usage or quota for these models."
- **`--ui-server`.** **Unverified** under BYOK: ADR 0030's live observer was not probed here.
- **Hooks.** Client-side; **unverified** on a local model.

## Other model servers

What each harness needs, against what each server says it serves:

| Server | Anthropic Messages (Claude Code) | Responses (Codex) | Chat Completions (Copilot default) |
| --- | --- | --- | --- |
| Ollama 0.40.17 | Yes, `/v1/messages`; no `count_tokens`, no prompt caching ([doc](https://github.com/ollama/ollama/blob/v0.40.17/docs/api/anthropic-compatibility.mdx)) | Yes, stateless ([doc](https://github.com/ollama/ollama/blob/v0.40.17/docs/api/openai-compatibility.mdx)) | Yes |
| LM Studio | Yes, "Anthropic-compatible Messages" ([docs](https://lmstudio.ai/docs/developer/anthropic-compat)) | Yes ([docs](https://lmstudio.ai/docs/developer/openai-compat/responses)); Codex has a built-in `lmstudio` provider on port 1234 | Yes |
| vLLM | Yes, `/v1/messages` and `/v1/messages/count_tokens` ([online serving](https://docs.vllm.ai/en/latest/serving/online_serving/)) | Yes, `/v1/responses` | Yes |
| OpenRouter | Yes: `ANTHROPIC_BASE_URL=https://openrouter.ai/api`, `ANTHROPIC_AUTH_TOKEN=<key>`, `ANTHROPIC_API_KEY=""`; "only guaranteed to work with the Anthropic first-party provider" ([guide](https://openrouter.ai/docs/guides/guides/claude-code-integration)) | **Unverified** from a primary source | Yes (its main API) |

All four are named supporters of [Open Responses](https://openresponses.org/), the open spec based on OpenAI's Responses API. The spec lets a provider implement a subset, and Ollama's is stateless. I did not check whether Codex's Responses requests stay within the Open Responses subset each server implements. None of these servers was run here except Ollama.

## Isolation: one ship on Ollama, another on the vendor, same config directory

Admiral already sets env per tmux session (`set-environment -t <session>`, and `-r` to hide a global name: `internal/sessionhost/server.go`). The chat already drops global `COPILOT_*` and `GH_*` names for Copilot (`CopilotEnvKept` in `internal/launcher/chat_copilot.go`). All three harnesses take their provider from env vars or command-line flags, so a per-ship route needs no config file. The leaks to guard:

### Claude Code

- **`settings.json` env.** Each `env` entry in a settings file "is written into Claude Code's process environment as given, replacing the value inherited from your shell in most sessions" ([env-vars](https://code.claude.com/docs/en/env-vars)). If the engineer's settings (user, project or managed) set `ANTHROPIC_BASE_URL`, an auth variable or a provider switch, a ship's env loses. `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1` makes Claude Code ignore those keys in settings files. It does not cover the `ANTHROPIC_DEFAULT_*_MODEL` family in *user* settings: the doc names model variables only "in a managed `env` block". Whether a user-settings `ANTHROPIC_DEFAULT_HAIKU_MODEL` would override the ship's is **unverified**. On this Mac the engineer's user `settings.json` env holds only proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`), and it does set `model`; `--model` on the command line wins over that. A proxy in settings applies to the model server too, unless `NO_PROXY` covers its address. **Unverified** here.
- **Login.** The claude.ai login stays saved and is not sent while `ANTHROPIC_AUTH_TOKEN` is set. If the ship sets only `ANTHROPIC_BASE_URL`, the engineer's OAuth token goes to the model server ([LLM gateways](https://code.claude.com/docs/en/llm-gateway)).
- **History and resume.** Transcripts share `projects/<cwd>/`, and the prompt history file is shared. The provider lives only in env, not in the transcript, which records the model name. So resuming a model-server ship's session from a vendor-env pane would carry on, on Anthropic, with the whole local conversation sent there. **Unverified** (not run, to avoid a paid call). Admiral would have to resume a ship with the env it launched with.
- **Child processes.** Bash-tool commands, hooks and anything a ship starts (an `admiral` CLI call, a crew boat, a nested `claude`) inherit `ANTHROPIC_BASE_URL` and the auth token. Admiral's hooks and anything it launches from a ship's env would have to clear them, or route on purpose.
- **tmux global env.** Admiral's tmux server outlives Admiral. A provider variable set with `set-environment -g`, or inherited by the tmux server, would ride into every ship. The provider env has to be per session, and vendor ships should `-r` the provider names. That is the chat's existing pattern, extended to Claude and Codex.
- **Gateway model cache.** Gateway model discovery (`CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY`) caches the gateway's `/v1/models` list in `<config dir>/cache/gateway-models.json`, a file that is shared per config dir ([gateway guide](https://code.claude.com/docs/en/llm-gateway-protocol)). It is opt-in, and `ollama launch` doesn't set it.

### Codex CLI

- **Flags only.** With `--oss`, or with `-c model_provider=…` (plus `-c model_providers.X.*` for a custom host), nothing is written to `CODEX_HOME`, as observed. The engineer's `config.toml` here sets `model = "gpt-6-sol"` and no `model_provider`; the ship's `-m` overrides the model.
- **Avoid `ollama launch codex` for ships.** It writes into the engineer's `~/.codex` whatever `CODEX_HOME` says, and it refuses the `-m` and `-c` flags Admiral's launcher would add.
- **History.** Rollouts share `sessions/` and the SQLite state. The resume picker filters by provider by default: `picker_provider_filter` uses the current `history_model_provider` (`tui/src/resume_picker.rs`). So the engineer's normal `codex resume` won't list Ollama threads. **Unverified:** what `codex resume <id>` of an Ollama thread does when run without the Ollama provider.
- **Login.** `auth.json` (the engineer's ChatGPT login) is untouched. The OSS provider doesn't use it, since `requires_openai_auth: false`.
- **App-server.** Per ADR 0030, the provider must go on `codex app-server -c …` in the ship's wrapper. The server is per ship, so that is per ship too.

### Copilot CLI

- **Env wins.** `COPILOT_PROVIDER_*` variables win over a saved `providers.json`, unless `COPILOT_PROVIDERS_CONFIG` names a file. A vendor ship must `-r` every `COPILOT_PROVIDER_*` name and `COPILOT_MODEL` from tmux's global env, as `CopilotEnvKept` already does for the chat.
- **Shared session store.** `session-state/` and `data.db` are shared in `COPILOT_HOME`. The provider is recorded per session in `events.jsonl`. **Unverified:** whether `--resume` of a BYOK session in a vendor-env pane switches the session to GitHub's models.
- **`/model` in any ship.** A vendor ship's `/model` also lists the discovered local Ollama models. If the engineer adds one there, it is saved to the shared `providers.json` and appears in every ship of that home. That is the engineer's choice, not a leak Admiral makes.
- **GitHub login.** Not required under BYOK. **Unverified:** whether a saved GitHub login is still used for the GitHub MCP server or web tools. `COPILOT_OFFLINE=true` would cut it, and with it web tools and the GitHub MCP server.

## Things that hold for all three

- **Context.** The local server's context (32k here) is smaller than the harness assumes, and each harness's own system prompt and tools take 12k–28k input tokens on the first turn here. The engineer has to raise the server's context: Ollama recommends 64k or more for all three. Admiral, or the dock, would have to tell the harness the real size: `CLAUDE_CODE_MAX_CONTEXT_TOKENS`, Codex's `model_catalog_json`, `COPILOT_PROVIDER_MAX_PROMPT_TOKENS`.
- **Usage and cost.** Claude Code invents a dollar cost. Codex and Copilot report tokens and no cost or quota. Nothing reports plan limits.
- **`ollama launch` is a launcher, not a protocol.** For Claude and Copilot it sets env and runs the harness as its child, so the pane's process is `ollama` with the harness under it. For Codex it also writes into `~/.codex`. Every route it uses is a documented harness setting Admiral could set itself. Its interactive selectors need `--yes --model` to run unattended.
- **Tool calling.** This is the model's skill, not the protocol's: the 4B model here made tool calls in all three, and picked the wrong file once in Claude Code.

## Unverified

- A Claude Code subagent actually spawned on a local model.
- What Claude Code shows when a tier alias is left unmapped and Ollama gets a `claude-*` id.
- Whether a user-settings `ANTHROPIC_DEFAULT_*_MODEL` overrides a ship's env under `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1`.
- The Claude Code TUI on a model server with the engineer's existing config dir, which has onboarding done.
- Ollama's behavior when a request exceeds its loaded context.
- Resuming a model-server session from a vendor-env pane, for all three.
- Codex `--remote` with an app-server started with `-c model_provider="ollama"`, and the observing client's events.
- Copilot `--ui-server` under BYOK.
- Hooks on Codex and Copilot with a local model.
- Whether Copilot uses a saved GitHub login under BYOK.
- Codex against OpenRouter's Responses endpoint.
- Codex's requests against each server's Open Responses subset.
- LM Studio, vLLM and OpenRouter in practice: none was run.
