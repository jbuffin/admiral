# Admiral

Visual dashboard for Commodore Launcher, the front end to the `commodore` command. Design phase; designs live in `designs/`.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for jbuffin/admiral. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Building Admiral

Before writing code, read in this order:

1. `GLOSSARY.md`: the words Admiral uses and how they map to Commodore Launcher's.
2. `designs/prototype/index.html` (open it in a browser) and `designs/README.md`: the reference for look, layout and motion.
3. `docs/spec.md`: each behavior with a "done when" check. Behavior wins over the prototype; the prototype wins on visual detail.
4. `docs/architecture.md`, `docs/adr/` and `docs/open-questions.md`. Do not build a part that depends on an open question until it is resolved and recorded.

Admiral calls Commodore Launcher (`commodore open`, `commodore tasks … --json`); it never reimplements profiles, repository lookup or session resume, and never writes the launcher's `config.json` or `state.db`. Touch must work on iPad Safari. Use the harnesses' official logos on flags; never redraw them.
