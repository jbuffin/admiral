# 9. Ships run in a checkout picked from the fleet's search roots

Status: accepted, amended by [ADR 0027](0027-campaigns-are-read-from-githubs-issue-tree.md) (a campaign's work repo comes first in the preselect) and by the amendment below (Admiral clones a missing repo on launch)

## Context

An order is freeform text, so Admiral cannot read a repository from it ([Orders that are not GitHub issues or PRs](https://github.com/jbuffin/admiral/issues/4)). Launching a ship needs a working directory, and the directory also decides which project skills the harness can see: all three harnesses read project skills from the working directory up to the repository root. Commodore, the default skill, makes its own git worktrees for its teammates, and Commodore Launcher started the harness in the checkout as it was.

## Decision

The engineer picks the ship's directory in launch orders from a list Admiral builds. Admiral never accepts a typed path, and it never creates its own worktrees ([Where a ship's session runs](https://github.com/jbuffin/admiral/issues/12)).

- **Search roots belong to the fleet.** Each fleet lists its own search roots, so work and personal checkouts never mix.
- **The list holds git checkouts and search roots, nothing else.** Admiral looks up to two levels below each search root and stops at a git checkout, never searching inside one. Checkouts come first in the list, the most recently launched into first, on any of the fleet's docks. The fleet's search roots sit at the bottom, so they show when the filter matches no checkout. A plain folder that is not a search root cannot be chosen.
- **The directory field is a searchable dropdown above the skill choice.** Typing filters the list by name.
- **Pre-selection**, first match wins:
  1. the order's `repo` hint from its order source, when it matches a listed checkout's git remote;
  2. a URL in the order's text whose repository matches a checkout's git remote;
  3. the directory last used on this dock.

  Matching is by remote only, never by folder names in free text. When several checkouts match, the most recently used is selected and the rest go to the top of the list.
- **The ship runs in the directory as it is.** If another ship is already in that directory, launch orders warns and still allows the launch.
- **A checkout that has vanished since the list was built fails the launch visibly**, and no ship is put to sea. Admiral never clones or creates directories.

## Considered options

- **A list from search roots** (chosen). One drop, a glance at the pre-selection, then Hoist sails, with no paths to type.
- **A typed path with type-ahead.** It reaches any folder, but the engineer does not want to type paths, and a typo becomes a failed launch.
- **A fixed directory per fleet.** It needs no choice at launch, but a fleet's orders span many repositories.
- **A git worktree per ship.** It keeps ships on one repository apart, but Commodore and other skills already make worktrees when they need them, and an Admiral worktree around them would nest them.

## Consequences

- To use a folder outside the search roots, the engineer adds a search root to the fleet.
- Two ships in one checkout can step on each other. The warning is the only guard.
- The order source plugin contract gains an optional `repo` on each order in `orders.list`: a repository URL, matched against checkout remotes like a URL in the order's text. The MVP's freeform list leaves it empty.
- A harness's first run in a directory may stop on its folder-trust prompt. The engineer answers it on the bridge, and ADR 0007's no-first-hook rule shows the ship as needs-you meanwhile.

## Amendment (2026-11-10)

From [Grilling: how do order sources work per fleet, with GitHub built in?](https://github.com/jbuffin/admiral/issues/227): **Admiral clones a missing repo on launch.** Launching on a repo with no checkout in the fleet runs `gh repo clone` into the fleet's `clone_root` (by default its first search root), and launch orders says so beforehand. An existing folder at that path fails the launch visibly and is never overwritten. The new checkout makes the repo a project of that fleet ([ADR 0006](0006-order-sources-are-subprocess-plugins.md)). This replaces "Admiral never clones or creates directories" above. Admiral still makes no worktrees and takes no typed paths.

From [Grilling: how does the campaign model work?](https://github.com/jbuffin/admiral/issues/229): a launch on a campaign child preselects the campaign's work repo first, before the order's `repo` hint ([ADR 0027](0027-campaigns-are-read-from-githubs-issue-tree.md)).
