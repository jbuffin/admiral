# Crew resolution fixtures

Language-neutral cases for the crew member resolver. Commodore's resolver (`skills/engineering/commodore/scripts/crew.py`) and Admiral's Go resolver (admiral#296) both run every case, so the layer rules stay in step. The rules themselves are in `skills/engineering/commodore/references/crew-members.md`.

## Case layout

Each directory under `cases/` is one case:

- `case.json`: `{"description": "...", "admiral": true|false}`, plus `"run": "<id>"` in a case that has `layers/ad-hoc/`, and `"repo": "<owner>/<repo>"` in a case that loads repo notes. `admiral` says whether the case runs inside an Admiral ship. `run` is the run id the resolver filters ad hoc members by: an opaque string, compared exactly. `repo` is the repo the notes belong to.
- `layers/<label>/`: the layer directories that exist for the case, named by layer label: `built-in`, `fleet`, `user-global`, `repo`, `run`, `ad-hoc` for the ad hoc directory (described below) and `memory` for the crew notes root (see "Crew notes"). A label with no directory is an empty layer. The built-in layer is a small made-up set, not a copy of the shipped `crew/` directory.
- `expected.json`: the resolver's stdout object with every `message` removed, plus `"exit"` (the process exit code). Exit 0 has `members` and `warnings`. Exit 1 has `errors` and `warnings`. Every member has `"notes": {"fleet": [...], "repo": [...]}`.

Run a case by mapping each `layers/<label>/` directory onto its layer. In Commodore's resolver that is `crew.py resolve [--admiral] --layer <label>=<dir> ...`. In Admiral, the layers map to these inputs:

- `fleet`: `ADMIRAL_CREW_DIR`, the fleet crew directory.
- `run`: `ADMIRAL_RUN_CREW`, the run layer directory Admiral writes pins to.
- `user-global`: `${XDG_CONFIG_HOME:-~/.config}/commodore/crew/`, read only outside Admiral.
- `repo`: `<repo>/.commodore/crew/`.
- `built-in`: the built-in members the resolver ships with.
- `ad-hoc`: `<ADMIRAL_CREW_DIR>/ad-hoc/` inside Admiral, `${XDG_CONFIG_HOME:-~/.config}/commodore/crew/ad-hoc/` outside it. The run id is the real path of Commodore's run directory. The fixtures write a short literal `run` instead.
- `memory`: the crew notes root: `<ADMIRAL_CREW_DIR>/memory/` inside Admiral, `${XDG_CONFIG_HOME:-~/.config}/commodore/crew/memory/` outside it. In Commodore's explicit mode, `--layer memory=<dir>`. The case's `repo` is `<owner>/<repo>`: Commodore's `--repo`, or the `origin` remote (`https://github.com/<owner>/<repo>.git`) of the repo in `--run` mode.

Inside Admiral (`admiral: true`) the `user-global` layer is ignored. Outside it (`admiral: false`) the `fleet` layer is ignored. Some cases put a member in the ignored layer on purpose, so a resolver handed an ignored layer's directory must ignore it. Commodore's explicit `--layer` mode rejects an ignored label with exit 5, so its eval leaves those directories out, and its `--run` tests cover the ignored layers.

## The ad hoc directory

`layers/ad-hoc/` is not an override layer. The resolver reads it after the run layer, and a member in it never replaces another member. These rules are part of the contract:

- `ad-hoc` comes last in layer order, so its warnings sort last.
- Filter first, silently. A file is this run's only when it parses with no error, has `ad-hoc: true` and has `ad-hoc-run` equal to the case's `run`, compared as exact strings. Every other file is skipped with no error and no warning, because it may belong to another run. That covers files that can't be read or parsed.
- The exception: a skipped file whose raw text has a line `ad-hoc-run: "<run>"` or `ad-hoc-run: <run>` gets one `ad-hoc-unreadable` warning.
- A member of this run gets the usual field warnings with `layer: "ad-hoc"`. `misplaced-ad-hoc` doesn't apply in this directory.
- A member whose name matches a standing member from any layer is refused with `ad-hoc-name-taken`. A member with role `fix-checker` is refused with `ad-hoc-fix-checker`.
- A directory that is a symlink, or a missing one, is skipped: a symlink with one `ad-hoc-dir-refused` warning (`file` and `field` null), a missing directory silently. A directory that can't be listed is one `ad-hoc-unreadable` warning with `file` null.
- A case with `layers/ad-hoc/` has a `run`. Write `ad-hoc-run` double-quoted in the case files.

## Crew notes

`layers/memory/` is the crew notes root, laid out `<name>/fleet.md` and `<name>/repos/<owner>/<repo>.md`. The rules are in `crew-members.md`, under "Crew notes". Part of the contract:

- Notes belong to a member's `name`, whatever layer the member came from.
- A `memory` warning's `file` is the path relative to the memory root (`implementer/fleet.md`), or null for the root or the repo. Every other layer's `file` is a basename. `memory` sorts after `ad-hoc`.
- Which memory root a run reads (the fleet's inside Admiral, the user-global one outside) is a `--run` rule, so no case covers it. The layer listing skips `memory/`.
- A case with no `repo` loads no repo notes.
- A member with role `fix-checker` gets no notes, whatever its name.
- Memory warnings, `bad-repo` included, come only when the memory root exists and at least one resolved member isn't the fix checker. `notes-bad-repo-no-root` pins this: a bad `repo` and no `layers/memory/` give no warning.
- A `memory-refused` case holds a committed symlink (see Symlinks). `memory-unreadable` and a symlinked memory root or member directory are generated inputs.

## Comparing results

Parse both JSON objects, drop every `message` field from your output's `errors` and `warnings`, then compare the structures and the exit code. Don't compare bytes: whitespace, key order and message text are free to differ.

## Symlinks

Some cases hold committed git symlinks (for example `error-outside-layer`). Check out or copy a case with symlinks kept, or those cases give the wrong result.

## Generated inputs

Inputs that can't be committed safely aren't fixtures: non-UTF-8 content (a note file too), a byte order mark, CRLF line endings, upper-case extensions, non-UTF-8 file names and files or directories with no read permission (the `unreadable` error). `evals/commodore/test_crew_resolution.py` generates those files at run time. Cover them in your own tests.

## Markdown lint

`cases/.markdownlint-cli2.jsonc` turns markdownlint off under `cases/`, because the error cases hold invalid frontmatter on purpose. This README is still linted.
