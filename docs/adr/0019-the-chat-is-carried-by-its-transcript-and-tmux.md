# 19. The chat is carried by its transcript and tmux

Status: accepted

## Context

[ADR 0018](0018-the-chat-is-one-long-lived-session.md) makes the chat one Claude Code session on Admiral's tmux socket. The web UI needs three things from it: the conversation to show, whether AdmiralBot is working, idle or waiting, and a way to send a message. The engineer decided how for this build ([#119](https://github.com/jbuffin/admiral/issues/119)): read Claude Code's transcript, and drive the pane with tmux.

This touches three earlier decisions. [ADR 0007](0007-needs-you-comes-from-harness-hooks.md) takes state from hooks only, because transcripts are undocumented and differ per harness. [ADR 0016](0016-answers-to-inbox-items-are-files-beside-the-item.md) says no hook calls the server. [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md) says Admiral records no conversation IDs.

## Decision

**Admiral learns the transcript's path from a hook, through tmux, and tails the file.**

- **The path arrives in a pane option.** The chat's hook bundle (`$ADMIRAL_HOME/harness/chat-claude-settings.json`, a second bundle beside the ships') registers `SessionStart`. `admiral hook SessionStart` writes one option on `$TMUX_PANE`, `@admiral-transcript`, holding a time stamp, the hook's `source` and the payload's `transcript_path`. It is the way `@admiral-state` reaches Admiral. No hook calls the server, so ADR 0016's last bullet holds. Ships' bundles don't register `SessionStart`, so ships are untouched.
- **Admiral reads the transcript for the chat only, never for a ship.** This is the one place ADR 0007's rule bends. State still comes from hooks (`working`, `needs_you` with a reason, which the chat service maps to `working`, `idle` and `waiting`), and only the text of the conversation comes from the file.
- **The file is Claude Code's undocumented JSONL, and Admiral owns that risk.** A line that doesn't parse, has no `uuid`, or is over 1 MiB is skipped, and a skipped line never fails the chat. If Claude Code changes the format, the conversation goes blank or short while the terminal still works.
- **The path is untrusted.** Anything in the chat session can write a pane option. Admiral takes only an absolute path ending in `.jsonl` under `<Claude config dir>/projects/`, where the config dir is the chat dock's `CLAUDE_CONFIG_DIR`, else Admiral's own, else `~/.claude`. It refuses the file or any directory below `projects/` that is a symlink, and a file that isn't regular. It reads from the last offset, and serves the last 200 messages and about 256 KiB of text.
- **The conversation is what the user and the assistant said.** User text and assistant `text` blocks only. Tool use, tool results, thinking, meta lines, sidechains, system lines and the local-command lines `/clear` leaves are left out.
- **Sending is a paste.** A message goes into the pane as one bracketed paste through a named tmux buffer (the text on stdin, never on a command line), then Enter. New chat sends `/clear`. The answer to a question will come through `admiral chat ask`, a file in its own directory (the shape of ADR 0016, never the inbox), and is built in a later unit.
- **Open terminal** is the real TUI through the bridge, as `_setup` is.

**Lifecycle.** ADR 0018 says Admiral "starts it and keeps it running". Admiral starts `_chat` when the UI first asks (`POST /api/chat/start`), not at server start, which saves an idle Claude process for an engineer who never opens the chat. The session has no `remain-on-exit`, so when Claude exits the session ends, and the next start makes a new one. A change of the `chat` key doesn't restart a running session. It applies at the next start, and at New chat, which restarts the session when its model, effort or fleet differ from the config. These are assumptions, and each is reversible.

**ADR 0012.** Admiral still records no conversation IDs and never resumes. The transcript path holds Claude's session id, and it lives only in the chat pane's tmux option, for the chat only, and nothing uses it to resume. A restarted Admiral reads the option back and serves the same conversation. A dead tmux server loses it, as ADR 0018 says.

**The chat is never a ship.** `IsShip` refuses `_setup` and `_chat` by name, so the chat, whose Claude can run `tmux set` on its own session, can't make itself a ship by setting `@admiral-name`.

## Considered options

- **Transcript and tmux** (chosen). The conversation is Claude Code's own, with its context and tools, in a session the engineer can open in a terminal, and it needs no second process.
- **`claude -p` or `--output-format stream-json` for each message.** The output is a documented format. But each message is a new process with no live TUI to open, and context across messages needs `--resume`, which means recording conversation IDs (ADR 0012).
- **Reading the screen.** It needs no hook and no file, but it changes with every release (ADR 0007), and scrollback is not a conversation.

## Consequences

- A change to Claude Code's transcript can blank the conversation. Nothing else breaks.
- **Unverified:** that Claude Code keeps a multi-line bracketed paste as one message, and that Enter right after the end of the paste submits it; that a long pasted message is stored in the transcript in full, not as the TUI's `[Pasted text …]` placeholder; and that `/clear` gives a new transcript path. Admiral serves nothing from before a `SessionStart` with `source: "clear"` on the same path, so the conversation empties either way.
- On the first start in a new folder, Claude may stop on its folder-trust dialog before any hook fires, and a pasted message would land in it. Until a path has been recorded for the running session, the session carries an error telling the engineer to open the terminal, and sending is refused. Admiral doesn't pre-trust the folder, since that writes Claude's config (ADR 0007).
- New chat empties every browser's conversation at once, without waiting for the hook. A transcript is written asynchronously and may lag, so a message sent right after New chat can race the new path.
- The tail's offset lives in memory. A restarted Admiral reads a transcript from its start, and after a same-path `/clear` it shows lines from before the clear again. A new path after `/clear`, which is what Claude Code appears to do, is not affected.
