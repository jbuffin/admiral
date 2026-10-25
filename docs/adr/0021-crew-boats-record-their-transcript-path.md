# 21. Crew boats record their transcript path

Status: accepted

## Context

An engineer or a driver watching a crew boat wants its final message, and Admiral shows none of the conversation ([#175](https://github.com/jbuffin/admiral/issues/175)). Each harness writes a transcript file, and its hooks can name it. [ADR 0019](0019-the-chat-is-carried-by-its-transcript-and-tmux.md) records the path for the chat only, from `SessionStart`, which crew boats don't register (they share the ships' bundle). [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md) says "Admiral records no conversation IDs", and [ADR 0007](0007-needs-you-comes-from-harness-hooks.md) takes state from hooks only.

## Decision

**A crew boat's pane records the path of its harness's transcript, and `admiral crew list --json` shows it as `transcript`.**

- **Only the path, only for crew boats.** Admiral never opens or reads the file. A ship's pane and the chat never get the option, whatever their payloads carry: `admiral hook` appends one tmux `if -F` on the window's `@admiral-crew-name`, so tmux decides from the option the reader uses. The gate is `#{!=:#{@admiral-crew-name},}`, not the bare name: `if -F` reads "0", a valid crew name, as false.
- **The option is `@admiral-crew-transcript`, a pane option.** The other crew record fields are window options. A pane respawn keeps it. The reader takes it as a tail field of the crew row, before the subagents.
- **Which harnesses supply it.** Claude Code: `transcript_path` on any hook event. Codex: `transcript_path` on any event; the key is always present, possibly `null`, which records nothing (source: codex-rs `hooks/src/schema.rs`). Copilot: `transcriptPath` on a lead `agentStop` only. Copilot's hooks reference lists the field on `agentStop`, `subagentStart` and `subagentStop` without a description, so its shape (`.jsonl` or not) and whether a subagent event carries the subagent's path are **unverified**. If the shape isn't `.jsonl`, Copilot reads `null`. Others read `null` when no event has carried a path.
- **The path is untrusted.** It is validated on write and again on read. Both use `sessionhost.ValidTranscriptPath` (absolute, ends in `.jsonl`, at most 4096 bytes, no control character) plus a stricter crew check: valid UTF-8, no C1 control or bidi formatting character, and none of `'` `"` `\` `` ` `` `$` `#` `;` `{` `}`. That keeps the nested tmux command trivially quotable. Spaces stay allowed. An invalid or absent path leaves the option as it was; a stored value that fails the check reads as none. The rule lives in `sessionhost` (`ValidCrewTranscriptPath`) and both sides use it.

**ADR 0012.** The path holds the harness's session id. It lives only in the crew pane's tmux option, and nothing resumes from it, so Admiral still resumes nothing and records no conversation IDs of its own.

**ADR 0007.** State still comes from hooks. The path takes no part in any state. ADR 0007's considered option "Transcripts" (rejected as a state source) is not reopened.

## Considered options

- **Reading the transcript itself.** Rejected: the formats are undocumented and differ per harness, and the driver can read the file it is given.
- **Asking the harness for its transcript.** Rejected: no harness offers it to a running boat.
- **The chat's `SessionStart`-only rule.** Rejected: crew boats share the ships' hook bundle, which registers no `SessionStart`, and adding it there would make every ship's pane write the chat's `@admiral-transcript`.
