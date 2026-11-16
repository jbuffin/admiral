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
- The restricted profile denies creating a `.git` (folder, file, link or hard link, in any letter case) below every root a live ship can write, in `sandbox.filesystem.denyWrite` and as `Edit(//…)` deny rules covering the `.git` and all under it. A Draft fix ship gets `<checkout>/*/**/.git`, which leaves the root's own `.git` writable (a deny on it would stop `git commit` writing the index and locks), and `<tempDir>/**/.git`; an Investigate ship, whose checkout is denied whole, gets the temp dir one. The temp dir is the resolved path, because the sandbox resolves a glob's fixed prefix through symlinks. The sandbox checks only the paths of a rename, so without the temp dir deny a repo built there could be moved into the checkout. A writable root added to the profile later needs the same deny. Git goes into a submodule only when its work tree has a `.git`, so with none, no filter or fsmonitor a ship plants can run under the unsandboxed git that Claude Code, the repo's hooks or the engineer's prompt run with the ship's environment. The restricted probe tries a nested `git init` and a repo moved in from the temp dir on real Claude Code, and fails the harness if either leaves a `.git`.
- A gitlink, or a `.gitmodules` put into the index with plain sandboxed git (`hash-object -w` and `update-index --cacheinfo`, which the write deny on the file does not stop), stays plantable but is inert: with no `.git` git never goes in. The scan still refuses both at relaunch and Promote.
- Rejected: `diff.ignoreSubmodules`, because a `.gitmodules` with `ignore = none` beats it; `submodule.active` and `status.submoduleSummary`, which do not stop the child status; and blanking git's attribute sources (an empty-tree `GIT_ATTR_SOURCE` with `core.attributesFile=/dev/null`), which leaves a submodule's `info/attributes` open and would make restricted ships ignore their repo's `.gitattributes`. `core.fsmonitor=false` in the ship's environment stays as defence in depth.
- Nothing in Admiral or Claude Code populates submodules (`git submodule update --init`, `--recurse-submodules`) outside the sandbox today; one that did would need the same scrutiny.
- A clone with a nested repository the ship needed on purpose is refused. That is the intended cost.
