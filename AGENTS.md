# Admiral

Visual dashboard that launches coding-agent sessions in the background and lets the engineer watch and attach to them. The build has started; designs live in `designs/`.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for jbuffin/admiral. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Building Admiral

Before writing code, read in this order:

1. `GLOSSARY.md`: the words Admiral uses.
2. `designs/prototype/index.html` (open it in a browser) and `designs/README.md`: the reference for look, layout and motion.
3. [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what to build, as user stories and implementation decisions. Behavior wins over the prototype; the prototype wins on visual detail.
4. `docs/adr/`. Open questions are GitHub issues. Do not build a part that depends on one until it is resolved and recorded.

Admiral launches sessions itself (ADR 0003): it never calls `commodore` and never reads or writes `~/.commodore-launcher/`. Touch must work on iPad Safari. Use the harnesses' official logos on flags; never redraw them.

Test only what is likely to break: state logic, protocol handling, and the drag and bridge flows. Don't test markup, styling or one-line glue code, and don't write a test for every small scenario. Prefer a few end-to-end checks over many unit tests. This applies to the server as well as the UI.
