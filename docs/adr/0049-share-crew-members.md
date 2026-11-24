# 49. Crew members are shared as files in a public repo the engineer owns

Status: accepted

## Context

A crew member is one Markdown file in a fleet's crew directory, `$ADMIRAL_HOME/fleets/<fleet key>/crew/<name>.md`. Engineers want to give a good one to someone else, and to take one from someone else, without copying files by hand. The file format belongs to Commodore (`jbuffin/skills`, `references/crew-members.md`), not Admiral: Admiral reads it through `internal/crew` and the shared resolver fixtures. Beside the member file the crew directory holds `memory/` (notes, often naming private repos) and `ad-hoc/` (members for one run), and a fleet can be a work fleet whose members must never leave the Mac by accident. A member file is also a prompt: whatever it says, a ship's teammate will follow.

The [crew sharing design](../crew-sharing.md) has the full detail.

## Decision

**A member is shared as `crew/<name>.md` in a public GitHub repo its publisher owns, its share repo. Admiral publishes, imports and unpublishes one member file at a time, through the fleet's own `gh` on github.com, and every publish, import, update and unpublish is the engineer's click after reading the exact bytes.**

- **Packaging.** One file per member, `crew/<name>.md`, in the member format, plus a README the scaffold writes. There is no manifest: Admiral lists `crew/*.md` through the GitHub API.
- **github.com only.** A fleet needs a built-in GitHub source on github.com. GitHub Enterprise is later work.
- **Publishing is off until turned on.** Each fleet has a Crew sharing switch, off by default, which gates creating the share repo and Share only. Import, crew sources, updates, Make mine and Unpublish work with it off. The fleet also remembers its share repo and its crew sources.
- **Scaffold.** The first Share offers to create a public repo under the fleet's `gh` account, `admiral-crew` unless the engineer changes the name, with the README. Nothing names any particular user.
- **Publish.** Share on a fleet member shows the exact bytes that will go public, then commits that one file with `gh api`. Only the member file goes: never notes, never ad hoc fields, never a path on the Mac. A hidden character or a Mac path refuses the publish, always.
- **Secret scan with the engineer's gitleaks.** Admiral runs `gitleaks` on the exact bytes and has no scanner of its own. A hit refuses the publish, with no opt-out. When gitleaks isn't installed, the preview says how to install it (Admiral never installs it, [ADR 0040](0040-admiral-installs-commodore-as-a-harness-plugin.md)) and lets the engineer publish without a scan, for that publish only, behind a clear warning.
- **No re-sharing.** Share is refused for any member with a sidecar, attached or not. **Make mine** removes the sidecar: the member becomes the engineer's own, gets no more updates, and can then be shared.
- **Unpublish.** A member is published when its file is in the share repo's `crew/` listing; Admiral keeps no record of its own. Unpublish deletes that file through `gh api` after a confirm showing it.
- **Import.** A ref (`<owner>/<repo>/<name>`, optionally `@<ref>`) or a GitHub URL imports one member; adding a repo as a crew source lets the engineer browse its members. An import pins the commit SHA; a tag pointing at it is shown as a label only. The file is written into the fleet's crew directory like any member, with its provenance in a sidecar, `<name>.shared.json`, which neither resolver reads as a member.
- **Trust.** The format checks (`crew.CheckFile`) and the hidden-character check refuse a bad file. Skills the member names that aren't installed are listed as warnings and never installed. An imported member shows "shared by `<owner>`".
- **Never overwrite.** A name a fleet member or a built-in already has is refused, with a rename offered (`<name>-<owner>`), which changes the name field and the file name together.
- **Pinned updates.** An update is any change to the member's file on the source's default branch; tags don't matter. Admiral checks when the Crew page opens and once a day, one GitHub API call per source repo, and shows "update available". Updating shows a diff and needs the engineer's click. A local edit detaches the member from updates, and Admiral says so.
- **Commodore owns the sidecar schema.** It is defined in Commodore's `crew-members.md`, and `crew.py resolve` and `cast` show "shared by `<owner>/<repo>@<short sha>`" (and "edited here") from it. Shared fixtures in `jbuffin/skills`, vendored into Admiral, keep both resolvers reading it the same way. Admiral writes sidecars; Commodore only reads them.
- **Engineer only.** AdmiralBot, routines and ships can't publish, import, update or unpublish ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)): none of the new routes is on the actor guard's list.

## Considered options

- **A public repo the publisher owns** (chosen). Free, versioned, readable without Admiral, and the publisher controls it. GitHub already holds every engineer's identity.
- **A central registry or index.** Easier to search, but someone has to run it and moderate it, and it puts one user at the center.
- **A manifest file listing the members.** One more file to keep in step with `crew/`, which the contents API already lists.
- **One gist per member.** No layout to scaffold, but no folder to browse, and a gist's history and tags are weaker.
- **Following the latest version.** Fewer clicks, but a publisher's change would reach every importer's ships unread.
- **Following tags for updates.** Publishers would have to tag every change, and most won't; any change to the file on the default branch is simpler and misses nothing.
- **Publishing the file as it is on disk.** Simpler, but it can carry unknown fields and ad hoc fields; Admiral rebuilds the canonical file instead.
- **A secret scanner vendored in Go.** Self-contained, but a second, weaker copy of gitleaks' rules to maintain. The engineer's gitleaks keeps its rules current.
- **Re-sharing imports.** Lets good members spread, but blurs who wrote what; Make mine makes taking ownership a deliberate step.
- **Recording publishes locally.** Goes stale the moment the share repo is changed elsewhere; the repo's listing is the truth.
- **Provenance known only to Admiral.** Less to change in Commodore, but `crew.py cast` would show an imported member as the engineer's own.

## Consequences

- The crew directory holds files the resolvers must not read as members. Both already read only regular `*.md` files that don't start with a dot; the new shared fixtures pin the sidecar cases.
- The resolver's output gains `shared` on every member, so Commodore's fixtures change first and Admiral re-vendors them.
- Rename and Delete on the Crew page carry the sidecar with the member file.
- Publishing without gitleaks is possible but never silent, and never remembered.
- A fleet on GitHub Enterprise only can't share, for now.
- Sharing missions is not part of this decision.
