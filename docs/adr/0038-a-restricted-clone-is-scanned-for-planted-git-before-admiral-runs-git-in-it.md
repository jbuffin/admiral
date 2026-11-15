# 38. A restricted clone is scanned for planted git before Admiral runs git in it

Status: accepted

## Context

A restricted ship ([ADR 0026](0026-untrusted-work-runs-on-a-restricted-ship.md)) writes freely inside its own clone under `$ADMIRAL_HOME/checkouts/<session>`, and what it leaves there is untrusted. Git treats a `.git` below the root, or a gitlink with a `.gitmodules`, as a nested repository and reads that repository's own config, which can name an fsmonitor command or a filter driver. Admiral runs unsandboxed git over that clone when it relaunches the ship in it and when the engineer promotes its work. The relaunch check refused a gitlink and a foreign root `.gitmodules`, but never looked for a `.git` or `.gitmodules` deeper in the tree.

## Decision

**Admiral scans a restricted clone in Go before any git runs over it, and refuses one that holds a planted carrier.**

- The scan walks the work tree with `Lstat`, never following a link, and refuses a `.git` (file, folder or symlink) or a `.gitmodules` anywhere below the root, and a root `.git` that is not a real folder, whatever the case, including under `.admiral/`. It also refuses a gitlink (a mode other than 100644, 100755 or 120000) in the clone's index, its HEAD tree and, for Promote, the branch tip's tree. The root `.git` folder is the clone's own and is the only one skipped. A root `.gitmodules` is refused on a relaunch when it differs from the routine repository's; Promote leaves it to the gitlink check, because a `.gitmodules` with no gitlink is inert and any gitlink is refused.
- The refusal names the offending path, relative to the clone, cut to 200 raw bytes (before quoting), quoted with `%q` and followed by a marker when cut, so it cannot forge a line or flood the message.
- A relaunch (a `Session` that names an existing clone) runs the scan, and so does `promote.Read`. The Promote route answers `400` with the path; this is the one refusal there that names a file. The read after the stop, in the launcher, hits the same scan; its failure keeps the fixed `500` message and the log holds the path.
- Admiral's own git calls over the clone run no submodule filter: `ls-tree`, `ls-files -s` and `hash-object --no-filters` run none, and `neutralgit` blanks every configured filter and diff driver for the rest.
- Don't open a restricted clone, or any folder under it, in a shell or editor. A shell prompt or an editor's git integration runs git there with the engineer's config.

## Consequences

- Amends [ADR 0026](0026-untrusted-work-runs-on-a-restricted-ship.md): the kept-clone check covers a git carrier at any depth, and Promote refuses one too, naming its path.
- The scan does not fully close the race before reuse. `nameFor` refuses a relaunch while the ship's tmux session is listed, and the scan runs only after that, but a process the ship started outside the tmux session can outlive it and plant a carrier after the scan. Admiral has no seam that finds or ends a ship's process group. Reuse has no production caller today: only the wake path launches with a `Session`, and it never starts a restricted ship.
- Git that a live restricted ship runs with its own environment is not covered. The environment's git config cannot name a filter it does not know, and a submodule's attributes can also come from its own `info/attributes` or `core.attributesFile`. The sandbox is what contains that git, as in ADR 0026.
- A clone with a nested repository the ship needed on purpose is refused. That is the intended cost.
