# 49. Crew members are shared as files in a public repo the engineer owns

Status: accepted

## Context

A crew member is one Markdown file in a fleet's crew directory, `$ADMIRAL_HOME/fleets/<fleet key>/crew/<name>.md`. Engineers want to give a good one to someone else, and to take one from someone else, without copying files by hand. The file format belongs to Commodore (`jbuffin/skills`, `references/crew-members.md`), not Admiral: Admiral reads it through `internal/crew` and the shared resolver fixtures. Beside the member file the crew directory holds `memory/` (notes, often naming private repos) and `ad-hoc/` (members for one run), and a fleet can be a work fleet whose members must never leave the Mac by accident. A member file is also a prompt: whatever it says, a ship's teammate will follow.

The [crew sharing design](../crew-sharing.md) has the full detail.

## Decision

**A member is shared as `crew/<name>.md` in a public GitHub repo its publisher owns, its share repo. Admiral publishes and imports one member file at a time, through the fleet's own `gh`, and every publish, import and update is the engineer's click after reading the exact bytes.**

- **Packaging.** One file per member, `crew/<name>.md`, in the member format, plus a README the scaffold writes. There is no manifest: Admiral lists `crew/*.md` through the GitHub API.
- **Off until turned on.** Each fleet has a Crew sharing switch, off by default. While it is off, the fleet can't publish. The fleet also remembers its share repo and its crew sources.
- **Scaffold.** The first Share offers to create a public repo under the fleet's `gh` account, `admiral-crew` unless the engineer changes the name, with the README. Nothing names any particular user.
- **Publish.** Share on a fleet member shows the exact bytes that will go public, then commits that one file with `gh api`. Only the member file goes: never notes, never ad hoc fields, never a path on the Mac. A secret pattern or a hidden character refuses the publish.
- **Import.** A ref (`<owner>/<repo>/<name>`, optionally `@<ref>`) or a GitHub URL imports one member; adding a repo as a crew source lets the engineer browse its members. An import pins the commit SHA and shows a tag when one points at it. The file is written into the fleet's crew directory like any member, with its provenance in a sidecar, `<name>.shared.json`, which neither resolver reads.
- **Trust.** The format checks (`crew.CheckFile`) and the hidden-character check refuse a bad file. Skills the member names that aren't installed are listed as warnings and never installed. An imported member shows "shared by `<owner>`".
- **Never overwrite.** A name a fleet member or a built-in already has is refused, with a rename offered (`<name>-<owner>`), which changes the name field and the file name together.
- **Pinned updates.** Admiral checks a crew source when the Crew page opens and once a day, one GitHub API call per source repo, and shows "update available". Updating shows a diff and needs the engineer's click. A local edit detaches the member from updates, and Admiral says so.
- **Engineer only.** AdmiralBot, routines and ships can't publish, import or update ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)): none of the new routes is on the actor guard's list.

## Considered options

- **A public repo the publisher owns** (chosen). Free, versioned, readable without Admiral, and the publisher controls it. GitHub already holds every engineer's identity.
- **A central registry or index.** Easier to search, but someone has to run it and moderate it, and it puts one user at the center.
- **A manifest file listing the members.** One more file to keep in step with `crew/`, which the contents API already lists.
- **One gist per member.** No layout to scaffold, but no folder to browse, and a gist's history and tags are weaker.
- **Following the latest version.** Fewer clicks, but a publisher's change would reach every importer's ships unread.
- **Publishing the file as it is on disk.** Simpler, but it can carry unknown fields and ad hoc fields; Admiral rebuilds the canonical file instead.

## Consequences

- The crew directory holds files the resolvers must skip. Both already read only regular `*.md` files that don't start with a dot (`memberFiles` in `internal/crew/resolve.go`, `crew.py`'s member-file rule), and the shared `ignored-files` fixture pins other extensions. A fixture for the sidecar itself goes into `jbuffin/skills` first.
- Rename and Delete on the Crew page carry the sidecar with the member file.
- A fleet needs a built-in GitHub source on github.com to share. A fleet on GitHub Enterprise only can't, for now.
- Sharing missions is not part of this decision.
