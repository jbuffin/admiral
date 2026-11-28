# What a paired client can send to AdmiralBot's chat besides text

> Research for [#954](https://github.com/jbuffin/admiral/issues/954), 2026-11-27, against `main` at `c7c8382`. Line numbers are at that commit.

## Short answer

Nothing. The chat takes one UTF-8 text string per message, at most 16 KiB after cleaning. There is no attachment, upload, multipart or image route anywhere in the server, and the web composer reads only `text/plain` from the clipboard. A link is just text: AdmiralBot sees the URL but has no tool to open it, unless one of its Admiral MCP tools takes a URL (a GitHub issue URL on a launch, for example).

The Claude Code session behind AdmiralBot could take images (pasted or dragged in, or named by path for the Read tool), but AdmiralBot runs with every built-in tool removed and reads blocked outside its own directory, so adding images means changing both the route and the fence.

## What the route takes today

- **Route.** `POST /api/chat/{fleet}/messages` (`internal/chat/service.go:266`), also served as `/api/v1/chat/{fleet}/messages` because the server strips the `/v1` prefix before routing (`internal/server/server.go:97-98`, `:123`). Documented in `docs/api.md:412-414`; `docs/api.md:321` says every chat route is under `/api/v1`.
- **Body.** JSON `{"text": "..."}` and nothing else. The handler decodes a struct with only `Text` (`internal/chat/service.go:780-792`). Unknown fields are ignored, not refused.
- **Limits.**
  - The request body is capped at 1 MiB (`http.MaxBytesReader(..., 1<<20)`, `internal/chat/service.go:784`).
  - The cleaned text must be non-empty and at most `sessionhost.MaxPaste`, `16 << 10` = 16,384 bytes (`internal/sessionhost/server.go:579-580`, checked at `internal/chat/service.go:797-802`). Over it is `400` with "the message is N bytes; at most 16384".
  - `409` when the chat isn't running, has no transcript yet, or is refreshing its context (`internal/chat/service.go:803-811`, `docs/api.md:414`).
- **Cleaning.** `CleanMessage` turns CRLF and CR into LF, drops every C0 and C1 control character except LF and tab, and trims the edges (`internal/sessionhost/server.go:582-595`). It exists so an escape can't end the bracketed paste early.
- **No other input path.** No Go file under `internal/` or `cmd/` parses multipart or form files (the only `multipart` hit is unrelated, in `internal/cli/guard_scout.go`). The ship send route, `POST /api/ships/<session>/messages`, has the same text-only body and limits (`docs/api.md:201`, `:207`).

## How a message reaches the session

1. `send` cleans and checks the text, takes the chat's lock and finds the pane (`internal/chat/service.go:795-818`).
2. `pasteMessage` gives tmux 10 seconds (`internal/chat/service.go:921-925`).
3. `PasteMessage` loads the text into a random tmux buffer over stdin (never argv or a shell), pastes it as one bracketed paste (`paste-buffer -p -r`), and presses Enter (`internal/sessionhost/server.go:597-613`, `:532-539`).

The message is typed into AdmiralBot's Claude Code (or Codex) TUI as though the engineer pasted it. Admiral doesn't build a Messages API request, so there is nowhere to put an image content block.

## What the web composer sends

- `ChatComposer` renders the shared `MessageField` with a text box and a Send button only (`web/src/app/ChatPanel.tsx:410-421`).
- `MessageField` is a `<textarea>` (`web/src/app/Composer.tsx:236`). Its `onPaste` reads only `clipboardData.getData('text/plain')` and inserts that; an image on the clipboard gives no text and is ignored (`web/src/app/Composer.tsx:252-262`). There is no `<input type="file">`, drop handler or `FileReader` in the chat UI.
- `useChat.send` posts `{ text: typed }` to `${base}/messages` (`web/src/app/useChat.ts:49-59`).

## What comes back

The chat's history is built from the session transcript, and only text blocks are kept: `textOf` joins `type: "text"` blocks and drops the rest (`internal/conversation/claude.go:206-214`). An image the session did receive would not show in the served history.

## Why AdmiralBot can't read a file or open a link today

- **Claude chat.** It launches with `FenceArgs` (`internal/launcher/chat.go:227`): `--tools ""` (no built-in tools, so no Read, Bash or WebFetch), `--strict-mcp-config` with only Admiral's MCP server, `--permission-mode dontAsk` and `--setting-sources project` (`internal/launcher/bundle.go:142-148`). Its settings allow only `mcp__<server>__*`, deny Edit, Write and NotebookEdit, and set `blockReadsOutsideWorkingDirectories` (`internal/launcher/bundle.go:134-140`). Its working directory is `$ADMIRAL_HOME/chat/<fleet>/` (`internal/launcher/chat.go:156-157`).
- **Codex chat.** The fence turns off Codex's `view_image` and `image_generation` features among others (`internal/launcher/chat_codex.go:27-31`).

So a Mac file path or a URL in a message is only text to AdmiralBot.

## What the session itself could accept

From Claude Code's docs:

- **Images in the TUI.** You can drag and drop an image, paste one with `Ctrl+V` (`Cmd+V` in iTerm2), or name a path, as in "Analyze this image: /path/to/your/image.png" ([Common workflows: Work with images](https://code.claude.com/docs/en/common-workflows#work-with-images)). A pasted image becomes an `[Image #N]` chip at the cursor ([Interactive mode: General controls](https://code.claude.com/docs/en/interactive-mode#general-controls)).
- **The Read tool.** It returns PNG, JPEG, GIF and WebP as images Claude can see, resized to the model's limits, and refuses an image over 128 MB. It reads PDFs, in ranges past 10 pages, and notebooks. It refuses binary extensions such as `.heic` ([Tools reference: Read tool behavior](https://code.claude.com/docs/en/tools-reference#read-tool-behavior)). This is what a "file path on the Mac" relies on, and the chat doesn't have it.
- **`@` mentions.** `@path` includes a file's content when it fits the Read tool's limits (25,000 tokens by default; text over 256 KB is left out), and a directory as a listing. A file Read refuses arrives as its path only ([Common workflows: Reference files and directories](https://code.claude.com/docs/en/common-workflows#reference-files-and-directories)).
- **Model limits.** Images must be JPEG, PNG, GIF or WebP, at most 8000x8000 px. An API request takes up to 100 or 600 images, depending on the model's context window, and the first-party API caps a request at 128 MB ([Vision: Request limits, Supported formats](https://platform.claude.com/docs/en/build-with-claude/vision#request-limits)). Claude Code resizes images before sending them, so a client would not need to.

## Not verified

These were not tested against a live chat:

- Whether the TUI attaches an image when the bracketed paste is an image file path (a terminal's drag and drop pastes a path), with `--tools ""` in force.
- Whether `@path` includes content when Read is not among the session's tools, and whether `blockReadsOutsideWorkingDirectories` covers it.

Either one would decide whether images could be added just by putting a file in the chat's own directory and pasting its path, or only by adding a Read tool scoped to that directory to the fence.

## What adding it would take (for a later decision)

1. A route that takes the image bytes (a multipart or base64 field beside `text`), checks type (JPEG, PNG, GIF or WebP; HEIC would need converting, since Read refuses it) and size, and writes the file inside `$ADMIRAL_HOME/chat/<fleet>/` so the reads fence still holds.
2. A way for the session to see it: paste its path or `@path`, if the unverified behaviour above holds, or let the fence allow `Read` limited to that directory.
3. A history entry that shows the image, since `textOf` drops non-text blocks today.
