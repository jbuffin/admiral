# 16. Answers to inbox items are files beside the item

Status: accepted

## Context

Ships ask the engineer things: a question, a pending review, finding cards ([#112](https://github.com/jbuffin/admiral/issues/112), [#116](https://github.com/jbuffin/admiral/issues/116)). A ship writes each as an item file, `$ADMIRAL_HOME/inbox/<session>/<id>.json`, and a skill that resumes later has to find the engineer's answer. [ADR 0007](0007-needs-you-comes-from-harness-hooks.md) keeps ships and Admiral apart: ships write files and Admiral watches them.

## Decision

**The answer is a file next to the item, `<id>.answer.json`, and Admiral never edits the item.** An item with an answer file is answered, whoever wrote the file, and leaves the inbox. So does an item whose ship directory is deleted (ADR 0015's cleanup).

- **Written through a temp file and a rename** in the ship's directory (`.<id>.answer.json.tmp-<random>`, which no reader takes for an item or an answer). Admiral holds one lock, re-reads the item and checks that no answer exists before it renames, and removes the temp file on any failure. A ship directory that vanishes mid-answer is a 404, never a 500.
- **Items are read defensively.** Ships are untrusted. Admiral reads through an `os.Root` on `$ADMIRAL_HOME`, never follows a link, opens with `O_NONBLOCK`, reads at most 1 MiB, and skips a file whose name is not `<id>.json` with `<id>` matching `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`, whose `id` differs from its name, or that is not valid against #112's item format. A skipped file never hides another item. A ship that has ended but still has its files still shows its items.
- **Three answer shapes, one file.** Every file has `version` (1), `id`, `kind` and `answered` (RFC 3339, UTC).
  - `question`: `answers` maps each question id to `{"choice": "<label>" or ["<label>", ...], "other": "free text"}`, and an optional `note`. Every question needs an answer. A `choice` is an option label, a list only on a `multi` question, and `other` can stand alone.
  - `review`: `action` is `submitted` or `discarded`, and `event` is `COMMENT`, `APPROVE` or `REQUEST_CHANGES`.
  - `findings`: `cards` maps each card id to `{"decision": "approved"|"edited"|"rejected", "proposal": {"kind", "content"}}`; `proposal` is present only for `edited`. Send only writes this file; the skill posts to GitHub.
  - Admiral writes the question answer in this version. The review and findings answers are written by the same code with the same rules in a later unit.
- **The outcome log gets one record per answer.** This extends [ADR 0015](0015-outcomes-are-an-append-only-history-beside-tmux.md)'s events with `answered` (`item`, `kind`) now, and `review_submitted`, `review_discarded`, `card_approved`, `card_edited` and `card_rejected` with the review and findings actions.
- **Admiral watches the directories** with fsnotify (`$ADMIRAL_HOME`, `inbox/` and each ship's directory), so a new item, an answer or a removed directory changes what is served within about a second.
- **ADR 0007 still holds.** Items aren't ship state, and skills write them, not hooks. No hook calls the server.

## Considered options

- **An answer file beside the item** (chosen). The skill that resumes already reads that directory, and a crash leaves no half state.
- **Edit the item in place.** It races the ship that wrote it and loses the original question.
- **Keep answers in the outcome log or `state.json`.** The log is never read back (ADR 0015), and a skill would need Admiral's server to read its answer. [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md) keeps a live ship in tmux, not in a second table.

## Consequences

- A skill reads `<id>.answer.json` to resume, and a missing file means unanswered.
- An answer file written by hand hides its item, and a wrong one is not checked.
- The answer is the record. If the `answered` outcome write fails, the item is still answered and the log misses one line.
- Two Admirals on one home would race only in the gap between the check and the rename; the rename then replaces the first answer.
