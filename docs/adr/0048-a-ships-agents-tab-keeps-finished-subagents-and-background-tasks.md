# 48. A ship's Agents tab keeps finished subagents and background tasks

Status: accepted

## Context

The classic view listed a ship's running subagents beside its crew boats. The new shell's ship view shows crew chips only ([ADR 0029](0029-the-bridge-answers-simple-prompts-by-sending-the-harness-keys.md): "Subagents get no chip"), so the list was lost. The server also dropped a subagent the moment it stopped and never listed background shells or Monitors.

## Decision

- **A fourth ship tab, Agents,** beside Conversation, Terminal and PR, on iPad and phone alike. It lists crew boats, subagents, agent-team teammates and background tasks, running first, then finished newest first, and its label carries the running count. Subagents still get no chip; they get a tab. Like PR, the tab is the ship's and doesn't follow a picked crew boat.
- **Finished entries stay for the life of the ship's lead session.** A stop marks the entry finished with an end time instead of removing it. Claude Code's `Stop` finishes entries its `background_tasks` no longer lists as running, and Copilot's lead `agentStop` and `userPromptSubmitted` finish every running entry. Running and finished entries together stay within 64; a new entry on a full list drops the finished entry that ended first, and a list of 64 running entries refuses it.
- **A new lead session clears the list:** Copilot's `sessionStart` with source `new`, and Claude Code's `startup` or `clear`. A Claude ship's `SessionStart` runs only `admiral ship-note`, which stays state-free, and `admiral hook SessionStart` would rebind the ship's transcript, so the ship bundle gains a second `SessionStart` hook, `admiral hook ShipSessionStart`, that does nothing but clear the list. `resume` and `compact` keep it.
- **A background task stores its kind, id and times only.** Kinds are `shell` and `monitor`; no command, description, prompt or output is kept. Claude Code reports shells only at a `Stop`, so a shell's start is when Admiral first saw it. A Monitor is added by its `PostToolUse`, persistent or not; the separate Monitor list that holds a Stop is unchanged, and a lost session loses both ([ADR 0037](0037-a-lost-ship-is-recorded-and-resumable.md)).
- **The wire:** `subagents` keeps meaning running subagents and teammates, so the classic view is untouched; a new `agents` field carries every entry with `kind` and `end`. A moored or dead ship serves `[]`, since the list lives on the pane.

## Consequences

- ADR 0014's clearing rules change as its Status line says; ADR 0029's tab list gains Agents.
- An unseen stop is still ignored, so Claude Code's internal agents don't appear.
- The list is gone once the pane is, so a woken ship starts with no history.
