# 3. Admiral launches sessions itself, without Commodore Launcher

Status: accepted

## Context

The first design made Admiral a front end to Commodore Launcher: fleets read from the launcher's `config.json`, tasks and sessions from its `state.db`, and every launch went through `commodore open`. The launcher is a thin CLI. For a GitHub URL it picks a profile (executable, config directory, environment), finds the repository on disk, remembers the harness conversation so the same URL resumes it, and runs the harness on `/commodore <url>` in the terminal it was typed in. It has no server and no background sessions, it always starts Commodore, and it only takes GitHub issue and PR URLs.

Admiral needs the opposite on each point: sessions that run in the background and can be attached later, any skill, any task source, and a long-running process. Four of the first open questions existed only because Admiral went through the launcher, and each asked the launcher to become something its own ADRs chose it not to be. What the launcher would save Admiral (profiles, repository lookup, resume) is small. What makes the design rich (a lead that assembles a task force, shown as crew boats) comes from Commodore the skill, which Admiral can start directly.

## Decision

Admiral is a standalone session launcher. It does not call `commodore`, and does not read or write anything under `~/.commodore-launcher/`. It owns:

- **Fleets**: its own configuration of fleets, the harnesses each allows, and each harness's executable and environment.
- **Repositories**: finding the checkout an order runs in.
- **Launching**: starting the harness under the fleet's settings, in the checkout, on the chosen skill with the order's URL. Commodore is the default skill.
- **Sessions**: running each one in a tmux session Admiral owns (ADR 0002), and remembering its harness conversation.

Commodore Launcher's `docs/harnesses.md` is useful prior art for how each harness takes a prompt, names a skill, resumes and finds skills, but Admiral does not depend on it.

## Considered options

- **Front end to the launcher's CLI** (`commodore open`, `commodore tasks … --json`). Shares profiles and session state with the CLI, but needs the launcher to grow a tmux host, a skill flag and non-GitHub tasks, and keeps Admiral waiting on another tool's roadmap.
- **Read the launcher's `config.json` for profiles only.** Saves setting profiles up twice, but still couples Admiral to the launcher's file format for little gain.
- **Standalone** (chosen).

## Consequences

- Admiral and the launcher share no state. Opening the same issue from both starts two separate conversations.
- The engineer sets up fleets in Admiral, separately from any launcher profiles.
- Open questions about launcher support (skills other than Commodore, headless sessions) are gone; repository lookup, fleet setup and what a non-GitHub order runs against are now Admiral's to decide.
- ADR 0002 was revised from "a tmux host in the launcher, preferred" to "tmux sessions Admiral owns".
