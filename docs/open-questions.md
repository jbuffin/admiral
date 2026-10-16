# Open questions

Things the design assumes that Commodore Launcher does not support today, or that nobody has decided yet. Resolve each before building the part that depends on it, and record the answer (an ADR, or a `GLOSSARY.md` entry).

1. **Launching with a skill other than Commodore.** `commodore open` always starts the harness on its Commodore invocation (`/commodore <url>` for Claude Code). Launch orders offer Wayfinder, Triage and any custom skill. Options: a launcher flag such as `--skill NAME`, or Admiral starting the harness itself for non-Commodore skills (which would bypass the launcher's profile and session handling, so it is not recommended).

2. **Headless sessions.** See ADR 0002. Until the launcher has a tmux host, Admiral can only attach to sessions it started itself.

3. **Orders that are not GitHub issues or PRs.** `commodore open` takes only a GitHub issue or PR URL. Jira and Linear orders need either a GitHub issue to point at, or launcher support for other task kinds.

4. **Crew boats.** How does Admiral learn a session's teammates and subagents? Candidates: Commodore reporting its task force somewhere machine-readable; reading the harness transcript (Claude Code writes `<config>/projects/<dir>/<id>.jsonl`); tmux panes Commodore opens. Each crew boat also needs its own terminal stream for the bridge.

5. **Needs you.** How does Admiral know a session is waiting on the engineer? Candidates: harness hooks (Claude Code `Notification` / `Stop` hooks) writing a marker; transcript state; terminal idle plus a prompt pattern (fragile; the launcher's ADR 0022 moved away from screen reading for this reason).

6. **Finished ships.** When is a ship "docked" (mission complete): the harness exited, the PR merged, or Commodore said so? Should docked ships fade away after a while or stay until dismissed?

7. **Ship names.** The prototype generates names (Saltpaw, Morning Tern). Keep them stable per task, and decide whether the engineer can rename.

8. **Configured skill list.** Where does the list behind "Edit skill list" live: Admiral's own settings, or the launcher's config?

9. **Fleet colors for more than two profiles.** `designs/tokens.json` has a sequence; confirm the engineer can override per profile.

10. **Harness logos.** Which official files to use for Claude Code, GitHub Copilot CLI and Codex CLI, and their usage rules.
