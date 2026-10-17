# Open questions

Things the design leaves undecided. Each is tracked as a GitHub issue. Resolve each before building the part that depends on it, and record the answer (an ADR, or a `GLOSSARY.md` entry).

3. **Orders that are not GitHub issues or PRs** ([#4](https://github.com/jbuffin/admiral/issues/4)). A GitHub order names its repository; a Jira or Linear ticket doesn't. Which repository does such an order run in, and what URL or text goes in the launch prompt?

4. **Crew boats** ([#5](https://github.com/jbuffin/admiral/issues/5)). How does Admiral learn a session's teammates and subagents? Candidates: Commodore reporting its task force somewhere machine-readable; reading the harness transcript (Claude Code writes `<config>/projects/<dir>/<id>.jsonl`); tmux panes Commodore opens. Each crew boat also needs its own terminal stream for the bridge.

7. **Ship names** ([#8](https://github.com/jbuffin/admiral/issues/8)). The prototype generates names (Saltpaw, Morning Tern). Keep them stable per task, and decide whether the engineer can rename.

8. **Harness logos** ([#11](https://github.com/jbuffin/admiral/issues/11)). Which official files to use for Claude Code, GitHub Copilot CLI and Codex CLI, and their usage rules.

## Resolved

- **Launching with a skill other than Commodore** ([#2](https://github.com/jbuffin/admiral/issues/2)). Admiral starts the harness itself with the chosen skill in the launch prompt (ADR 0003, `docs/architecture.md`).
- **Headless sessions** ([#3](https://github.com/jbuffin/admiral/issues/3)). Each ship runs in a tmux session on Admiral's own socket (ADR 0002).
- **Configured skill list** ([#9](https://github.com/jbuffin/admiral/issues/9)). Replaced by up to five favorite skills kept in Admiral's configuration (`docs/spec.md` §4, ADR 0010).
- **Fleet colors for more than two fleets** ([#10](https://github.com/jbuffin/admiral/issues/10)). A fleet may set its color in Admiral's configuration; otherwise it takes the next color in `designs/tokens.json` (`docs/spec.md` §1).
- **Needs you** ([#6](https://github.com/jbuffin/admiral/issues/6)). Harness hooks added at launch write the state to a tmux pane option; Codex questions use the bell; no first hook within about 10 s means needs-you (ADR 0007).
- **Finished ships** ([#7](https://github.com/jbuffin/admiral/issues/7)). A ship is anchored once its harness exits: a clean exit sails off and disappears, a non-zero exit stays until dismissed; resuming is out of the MVP (ADR 0008).
- **Where a ship's session runs** ([#12](https://github.com/jbuffin/admiral/issues/12)). The engineer picks a git checkout, or a search root, from a list built from the fleet's search roots; pre-selected by the order source's hint, a URL matching a git remote, or the dock's last directory; no typed paths, no Admiral worktrees, no cloning (ADR 0009).
- **Setting up fleets** ([#13](https://github.com/jbuffin/admiral/issues/13)). `admiral setup` detects harnesses and writes `~/.admiral/config.json` (JSON, `version` 1), runnable from a terminal in the browser; the file is hand-editable and watched; running ships keep their launch settings (ADR 0010).
