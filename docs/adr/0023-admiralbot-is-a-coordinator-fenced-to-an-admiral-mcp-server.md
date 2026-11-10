# 23. AdmiralBot is a coordinator fenced to an Admiral MCP server

Status: accepted

## Context

[ADR 0018](0018-the-chat-is-one-long-lived-session.md) gives AdmiralBot one write, adding an order, and [ADR 0020](0020-admiralbot-reaches-admiral-only-through-the-admiral-cli.md) holds it to the `admiral` CLI with Claude Code allow rules. The redesign makes AdmiralBot each fleet's chief of staff ([#209](https://github.com/jbuffin/admiral/issues/209), [#212](https://github.com/jbuffin/admiral/issues/212)): the engineer says "check the AWS logs and find the cause of this error" from a phone, and work starts without an order in between. It also has to answer inbox items when told to ([#221](https://github.com/jbuffin/admiral/issues/221)), and it may run on Codex or Copilot CLI as well as Claude Code.

Research for [#222](https://github.com/jbuffin/admiral/issues/222) found that neither Codex nor Copilot CLI can refuse every shell command except `admiral …`: Codex runs an unmatched command in its sandbox under `approval_policy=never`, and Copilot prompts for it. All three harnesses can be held to the tools of one MCP server.

## Decision

**AdmiralBot does no work itself. It reads Admiral, talks to the engineer, and acts by launching ships. It reaches Admiral only through an Admiral MCP server, with every built-in tool off.**

- **One per fleet.** Each fleet has its own AdmiralBot chat ([#209](https://github.com/jbuffin/admiral/issues/209)). Its harness, model, effort and context threshold are set on that fleet's Preferences, in place of the top-level `chat` key. The harness list offers only harnesses whose fence has been proven on this machine ([#241](https://github.com/jbuffin/admiral/issues/241)); with none, the fleet has no AdmiralBot and the chat shows as off with the reason.
- **Writes.** Launch a ship in its own fleet, from an order or from freeform text, with a dock, skill (Commodore, a favorite, custom or none), checkout and text; a team is a launch whose ship brings crew. The launch also takes `crew`, members pinned by name, and `issues`, where two or more campaign children make one slice ([skills#195](https://github.com/jbuffin/skills/issues/195)). Add an order only to a plugin source that supports it; freeform orders are gone ([#227](https://github.com/jbuffin/admiral/issues/227)), so a request becomes a launch or a launch commitment instead. Answer a file item, and give a finished turn its next prompt ([#221](https://github.com/jbuffin/admiral/issues/221)). Stop a ship, and dismiss an anchored one. Write to its ledger ([ADR 0024](0024-admiralbot-memory-is-a-server-owned-ledger.md)). Turn Hold fire on, and turn a routine off. Ask a chat question. Draft a routine, which the engineer saves on the Routines screen.
- **Never.** Answer harness approvals or harness questions, type into a ship mid-turn, change config, sources, settings or Preferences, save or turn on a routine, or turn Hold fire off. It may make things quieter, never loosen a limit.
- **When it may act.** Only on the engineer's current instruction in the chat, or on a ledger commitment the engineer agreed to, including "work on #n", which follows a campaign ([#229](https://github.com/jbuffin/admiral/issues/229)). One instruction that maps to one action: it acts, then quotes what it did. Anything else: it proposes with a chat question.
- **Caps and resources.** A launch on a current instruction behaves like a hand launch: it counts toward the global cap but is never blocked by it, and holds no resources unless the engineer names one. A launch from a commitment runs unattended, so it waits for room under the global cap and for any resources it names, and the brief shows it waiting.
- **Identity and fleet.** Each fleet's chat gets its own actor token. The server stamps every write **via AdmiralBot** (outcomes, answers, a ship's trigger) and refuses any read or write that touches another fleet's ships, items, orders or ledger, naming the fleet. A call with no actor token is the engineer's own CLI.
- **Duplicates and claims.** Every launch carries an idempotency key: the order id, the issue URL, or `chat:<message id>` for freeform text. The server refuses a second launch while a live ship holds the key, and names that ship. A launch from an issue takes a server-side claim on it, released when the ship sails off or is dismissed. A slice claims every child, and its key is the sorted set of their URLs. "Launch another anyway" gets a fresh key. Admiral never assigns the issue on GitHub.
- **The fence.** Admiral starts the MCP server over stdio with the chat's actor token; it talks to the Admiral server on loopback. Its typed tools mirror the writes above and the reads. Every harness's built-in tools are off and only this server is allowed. Admiral builds and owns the fence; the engineer configures nothing.
  - Claude Code: `--tools ""`, `--strict-mcp-config --mcp-config`, `--permission-mode dontAsk`, allow `mcp__admiral__*`.
  - Codex: `features.shell_tool=false`, `web_search="disabled"`, `mcp_servers.admiral.default_tools_approval_mode="auto"`, and its own `CODEX_HOME`, since the interactive Codex can't skip the user's `config.toml`.
  - Copilot CLI: `--available-tools` to hide `bash` and the rest, `--allow-tool=admiral`, `--disable-builtin-mcps`, and its own `COPILOT_HOME`.
- **Ships are not fenced.** A ship AdmiralBot launches is the engineer's act at one remove: it starts with the fleet's normal setup, the engineer's harness config and MCP tools, as a hand launch does. Only work started by outside events runs restricted.
- **Autopilot mode.** Every Commodore ship AdmiralBot launches runs in autopilot mode unless the engineer says "with me", and so does every unattended launch: from a commitment, a campaign's Without-you children, a routine or a review round ([skills#195](https://github.com/jbuffin/skills/issues/195), [#240](https://github.com/jbuffin/admiral/issues/240)). AdmiralBot names autopilot in the line that reports the launch. Nothing merges on its own: the guard refuses every merge from an autopilot-mode ship, `gh pr merge --auto` included, and merging stays the engineer's click.
- **The `admiral` CLI stays for people.** It and the MCP server can share code.

## Considered options

- **An MCP server for every harness** (chosen). One typed interface, a fence each harness can enforce, and no shell at all. Typed tools also drop ADR 0020's heredoc line format and its 10,000-character limit.
- **The `admiral` CLI with allow rules** (ADR 0020). It works on Claude Code, but Codex and Copilot can't be held to it.
- **AdmiralBot doing work itself** (reading logs, editing code). It needs the tools the fence takes away, and its long-lived context would fill with the work. A ship does the work and its report comes back to the chat as a card.

## Consequences

- This supersedes ADR 0020's interface and fence, and amends ADR 0018: AdmiralBot is per fleet, set on Preferences, and its writes go beyond adding an order. ADR 0020's chat question files keep their shape; `chat ask` becomes an MCP tool.
- **Unverified:** whether the harness login survives a separate `CODEX_HOME` or `COPILOT_HOME`, and whether the engineer's plugin MCP servers stay out. Codex and Copilot show greyed out for AdmiralBot, with the reason, until a real session proves each fence.
- An MCP server is a second process per chat to start and keep in step with it.
