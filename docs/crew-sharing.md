# Crew sharing

How an engineer publishes a crew member to a public GitHub repo and imports one someone else published. The decision and its trade-offs are in [ADR 0049](adr/0049-share-crew-members.md); this file is the build spec. Read it before changing crew sharing, the crew directory's file rules, or the Crew page's Share and Import flows.

The member file format belongs to Commodore (`jbuffin/skills`, `skills/engineering/commodore/references/crew-members.md`). Admiral publishes and imports that format as it is and adds nothing to it. Provenance lives in a separate file. Sharing missions is out of scope (see [Future work](#future-work)).

## Words

- **Share repo**: the public GitHub repo a publisher owns and publishes members to. The fleet remembers it.
- **Crew source**: a public repo a fleet browses for members to import. Any repo with a `crew/` folder of member files is one, including the fleet's own share repo.
- **Shared member**: a fleet member imported from a crew source. Its sidecar records where it came from.
- **Sidecar**: `crew/<name>.shared.json` beside a shared member's file.
- **Detached**: a shared member whose file no longer matches the bytes Admiral wrote. It stays in the fleet but gets no updates.

## Share repo layout

```text
<owner>/<repo>            # public; default name admiral-crew
  README.md               # written by the scaffold; the publisher may edit it
  crew/
    reviewer.md           # one member file, the member format, nothing else
    test-writer.md
```

- There is no manifest. A member is a file `crew/<name>.md` whose stem is a valid member name (`crew.ValidName`); anything else in `crew/` is ignored when browsing (a subfolder, another extension, a dotfile).
- Admiral reads only `crew/` on the repo's default branch, or at the ref an import names.
- Admiral writes only `README.md` (at creation) and `crew/<name>.md` (on Share). It never deletes a file in the share repo: removing a member from it is the publisher's job on GitHub.
- The scaffold README says what the repo is, that each file in `crew/` is a crew member for Commodore and Admiral, and how to import one (the ref form below). It names no user: the owner and repo come from the repo being created.

## Refs

A ref names one member in a crew source:

```text
<owner>/<repo>/<name>             # the default branch's head
<owner>/<repo>/<name>@<ref>       # a branch, a tag or a commit SHA
```

The import box also takes these GitHub URLs:

- `https://github.com/<owner>/<repo>/blob/<ref>/crew/<name>.md`: one member at `<ref>`.
- `https://github.com/<owner>/<repo>/tree/<ref>/crew` and `https://github.com/<owner>/<repo>`: no member named, so the box offers to add the repo as a crew source and opens its browse list.

`<owner>` and `<repo>` follow GitHub's rules (`internal/github` has the repo-name check). `<name>` must pass `crew.ValidName`. A `.md` suffix on `<name>` is dropped. Anything else is a 400 that names the form, for example `octocat/admiral-crew/reviewer@v1`.

An import always resolves the ref to a commit SHA first and pins that. A tag is shown, not stored as the pin: if a tag in the repo points at the pinned commit, the Crew page shows it (`v1`), and the sidecar keeps it as a label.

## The sidecar

`$ADMIRAL_HOME/fleets/<key>/crew/<name>.shared.json`, written beside `<name>.md` when a member is imported and rewritten when it is updated. Mode 0600, written atomically through a dot temp file like the member file.

```json
{
  "version": 1,
  "host": "github.com",
  "repo": "octocat/admiral-crew",
  "path": "crew/reviewer.md",
  "upstream_name": "reviewer",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "tag": "v1",
  "upstream_blob": "89abcdef0123456789abcdef0123456789abcdef",
  "content_sha256": "<hex sha256 of the bytes Admiral wrote to <name>.md>",
  "imported_at": "2026-11-24T10:00:00Z",
  "updated_at": "2026-11-24T10:00:00Z"
}
```

- `repo` and `path` say where the file came from; `upstream_name` differs from the local name after a clash rename.
- `commit` is the pinned 40-hex commit SHA. `tag` is optional and only a label.
- `upstream_blob` is the git blob SHA of `path` at `commit`. The update check compares it with the blob SHA the contents listing returns (see [Updates](#updates)).
- `content_sha256` is the hash of the bytes Admiral wrote, after any clash rename. When `<name>.md` no longer hashes to it, the member is detached. Detached is computed on each read, never stored, so undoing a local edit by hand re-attaches it.
- "shared by `<owner>`" comes from `repo`'s owner part.
- Unknown fields are kept on rewrite. A sidecar that doesn't parse, has another `version`, or names a `path` outside `crew/` makes the member a plain fleet member with a warning on the Crew page; Admiral never deletes it on its own.

**Resolution ignores the sidecar.** Both resolvers read only regular `*.md` files that don't start with a dot (`memberFiles` in `internal/crew/resolve.go`; `crew.py`'s member-file rule), so `reviewer.shared.json` is never a member. The shared fixture `ignored-files` already pins other extensions; the build adds a sidecar case to `jbuffin/skills`'s fixtures first and vendors it with `sync.sh`, so both resolvers keep ignoring it.

A sidecar with no `<name>.md` beside it is an orphan. The Crew page doesn't show it; Delete and Rename never leave one behind.

## Fleet settings

A new object on the fleet in `config.json`, edited through `PUT /api/fleets/{key}` like the other fleet fields (`config.Manager.SetFleetFields`) and returned on the fleet view:

```json
"crew_sharing": {
  "enabled": false,
  "share_repo": "octocat/admiral-crew",
  "sources": ["octocat/admiral-crew", "hubot/crew"]
}
```

- `enabled` is false when the object is missing. Turning it on is a toggle on the fleet's Preferences page, with one sentence under it: "Lets you publish this fleet's crew members to a public GitHub repo."
- `share_repo` is set when the scaffold creates the repo or the engineer picks an existing one on first Share. It can be cleared or changed in Preferences.
- `sources` is the list of crew source repos, in the order added, without duplicates. Adding a source from the Crew page writes it here.
- Invalid values are a 400 naming the field, the way the other fleet fields are.

## GitHub access

Every call goes through the fleet's built-in GitHub source client, `sources.FleetGitHub(cfg, fleet)` in `internal/sources/github.go`, which runs `gh api` with the fleet's account and env (`internal/github.Client.rest`). No new token store, no second `gh` account, no HTTP client of Admiral's own.

- A fleet with no GitHub source can't share or import: the Crew page says "Connect GitHub for this fleet first" and links to the fleet's GitHub page.
- Only `github.com` is supported in the first build. A fleet whose GitHub source is on another host gets the same refusal with the host named (open question).
- Reading a public crew source uses the fleet's token too, which keeps the rate limit at 5,000 calls an hour.

The calls, all with `gh api --method <M> --hostname github.com [--input -] <endpoint>`:

| Step | Call |
| --- | --- |
| Who am I | `GET /user` (login, for the scaffold owner and "your repo") |
| Check share repo | `GET /repos/{owner}/{repo}` (`visibility` must be `public`, `permissions.push` true, `default_branch`) |
| Create share repo | `POST /user/repos` `{"name", "description", "private": false, "auto_init": false}` |
| Write README | `PUT /repos/{owner}/{repo}/contents/README.md` `{"message", "content"}` (the repo's first commit) |
| Publish a member | `PUT /repos/{owner}/{repo}/contents/crew/{name}.md` `{"message", "content", "sha"?}` (`sha` is the current blob when replacing; absent when new) |
| Resolve a ref | `GET /repos/{owner}/{repo}/commits/{ref}` (the commit SHA) |
| Read a member | `GET /repos/{owner}/{repo}/contents/crew/{name}.md?ref={sha}` |
| Find a tag | `GET /repos/{owner}/{repo}/tags?per_page=100` (first tag whose commit is the pin) |
| List a source | `GET /repos/{owner}/{repo}/contents/crew` (each file's name and blob `sha`, on the default branch) |

Bodies go on stdin, never `-F`, so the fake `gh` (`e2e/testdata/fakegh/gh`) serves every call. Each step's failure text names the step and the repo, with `gh`'s message after it.

The scaffold creates the repo only under the `gh` account itself (`/user/repos`), never under an organization, and only on the engineer's click, like [ADR 0047](adr/0047-admiral-adds-or-creates-a-fleets-project-through-one-route.md). A name already taken is a 409 that offers "Use the existing repo" when the account can push to it and it is public.

## Privacy checks

A publish sends exactly one file. Admiral builds its bytes and checks them before the preview, and checks them again on commit:

1. **Only fleet members.** Share is offered on a member whose layer is `fleet` and that has no attached sidecar. A built-in has no file to share (customize it first); an attached shared member belongs to its publisher. Memory notes, ad hoc members and run members are never offered.
2. **Canonical bytes.** Admiral rebuilds the file from the resolved member with `crewapi`'s canonical writer (`memberBody.file`), not the file on disk. That drops unknown fields and never writes `ad-hoc` or `ad-hoc-run`. The bytes pass `crew.CheckFile` before anything else.
3. **No paths from the Mac.** The bytes are refused when they contain `$ADMIRAL_HOME`, the user's home directory, the fleet's clone root or any of its search roots, as absolute paths. The refusal names the line.
4. **No hidden characters.** A rune that `textsafe.Hidden` flags, or whose category is one of crew notes' hidden set (Cc, Cf, Cs, Co, Cn, Zl, Zp), refuses the publish, except `\n` and `\t`. The refusal names the line and the code point (`U+202E on line 7`).
5. **No secrets.** A secret scanner runs over the bytes (see below). Any hit refuses the publish and names the line and the rule, never the matched text.

A refused publish has no Override. The engineer fixes the member (Edit on the Crew page) and tries again.

**The secret scanner.** Admiral has none in-process today, and `.gitleaks.toml` isn't run in CI. The build adds `internal/secretscan`: a small Go list of gitleaks-style rules (the default rules' high-signal subset: cloud keys, GitHub, Slack, Stripe, private key blocks, JWTs, generic `key|token|secret|password = <long value>`) and one rule for `decoy-secret-<hex>`. Tests prove a hit with `decoy-secret-<hex>` values, which `.gitleaks.toml` already allowlists, so no key-shaped string enters the tree. The import path runs the same scanner as a warning (a publisher's secret is their problem, but the engineer should know).

**Preview equals commit.** The preview route returns the bytes and their sha256. The publish route takes that sha256, rebuilds the bytes, runs every check again and commits only when the hash still matches; otherwise it's a 409 "The member changed since the preview" and the page shows the new preview.

## Import

Importing is reading a file from GitHub and writing it as a fleet member, with three extra rules.

- **Read the whole file first.** The import dialog shows the member's rendered card (name, role, based-on, skills, model, harness, pick-me-when) and the full raw file, with "shared by `<owner>`", the repo, the pinned short SHA and the tag. Nothing is written until the engineer clicks Import.
- **Refuse a bad file.** The bytes must be valid UTF-8, at most `crew`'s 64 KiB limit, free of hidden characters (the same check as publishing), and pass `crew.CheckFile` with the stem from the file name. A file with `ad-hoc` or `ad-hoc-run` is refused: those belong to a run, not a fleet. Each refusal says which rule and which line.
- **Never overwrite.** When the name is taken (a fleet member file exists, or a built-in has it), the dialog says so and offers `<name>-<owner>` (cut to the 64-character slug limit; when that is taken too, the engineer types one). Renaming rewrites the `name` field (`renamed` in `crewapi`) and the file name together, then runs `crew.CheckFile` again. The write uses a hard link so a file that appeared since the check is never replaced (`KeepMission`'s pattern).
- **Skills are warnings.** Each skill the member names that the fleet's dock can't see (`GET /api/fleets/{key}/crew/skills`) is listed: "Not installed: `<skill>`". Import is still allowed; Admiral never installs a skill.

The member file and sidecar are written under the crew write lock; the member first, then the sidecar. A failure between them leaves a plain fleet member, which the Crew page shows as one.

## Crew sources and browsing

- **Add a source** from the Crew page's Import dialog (a repo URL or `<owner>/<repo>`). Admiral checks the repo is reachable and has a `crew/` folder, then appends it to `crew_sharing.sources`.
- **Browse** lists a source's members from one contents listing call, then reads each file only when the engineer opens it. Each row shows the name and, once read, the role. A member already imported from this source and path shows "Imported" (and its update state) instead of Import.
- **Remove a source** removes it from the list. Members imported from it stay, still pinned; they keep their sidecars and their update checks stop.
- A member imported from a ref, not a source, still gets update checks: the checker covers every repo named by any sidecar in the fleet, plus the sources.

## Updates

**When.** Admiral checks when the Crew page opens and once a day in the background.

- The background check is a loop modeled on `internal/update.Checker`: started from `serve()`, the first check after a delay read by `internal/delay.From` (`ADMIRAL_CREW_UPDATE_FIRST_CHECK`, so tests can make it quick), then every 24 hours, with an injected clock. Results are kept in memory per fleet, not on disk.
- Opening the Crew page asks the checker for the fleet's state. A result younger than five minutes is reused; otherwise it checks now and the page shows "Checking for updates" on shared members until it answers. A failed check shows nothing new and logs the error; the next one retries.

**How.** One call per repo: `GET /repos/{owner}/{repo}/contents/crew` on the default branch. For each attached shared member from that repo, the listed blob SHA of its `path` is compared with its sidecar's `upstream_blob`:

- Same: up to date.
- Different: "Update available".
- Missing from the listing: "Removed upstream". The member stays as it is.
- The member is detached: no badge, and the detail says "Edited here, so it no longer gets updates from `<repo>`."

The badge shows on the member's list row and on the detail header.

**Update.** Clicking Update resolves the default branch to a commit SHA, reads the file there, and shows a line diff of the current file against the new one, with the same checks as an import (a file that fails one can't be applied). Apply writes the new bytes (with the local name kept, through `renamed` if it differs from upstream) and rewrites the sidecar's `commit`, `tag`, `upstream_blob`, `content_sha256` and `updated_at`. Nothing updates without that click.

**Detach.** Editing a shared member on the Crew page first warns "Editing detaches this member from `<repo>`. It won't get updates." A hand edit on disk detaches it silently, and the Crew page says so the next time it loads.

## API

All under `/api/fleets/{key}/crew/`, JSON in and out, the `?harness=` dock query where a member list is involved, like the existing crew routes. None is added to the actor guard's allowlist, so AdmiralBot, routines and ships get 403.

| Route | What |
| --- | --- |
| `GET  .../crew/sharing` | The fleet's `crew_sharing` state, the `gh` login, whether the share repo exists and is pushable, and update states per shared member. |
| `POST .../crew/sharing/repo` | Create the share repo and its README `{"name", "description"}`, or adopt an existing one `{"repo": "owner/name"}`. Saves `share_repo`. |
| `POST .../crew/members/{name}/share/preview` | The canonical bytes, their sha256, the target path, and whether it replaces a file. Runs every privacy check; a hit is a 422 naming the check, the line and the rule. |
| `POST .../crew/members/{name}/share` | `{"sha256"}` from the preview. Rechecks and commits. Returns the commit URL. |
| `POST .../crew/imports/preview` | `{"ref"}` (a ref or URL). Returns the raw bytes, the parsed card, `owner`, `repo`, `path`, `commit`, `tag`, the clash (with the suggested name), skill warnings and secret warnings. A URL with no member returns `{"source": "owner/repo"}` instead. |
| `POST .../crew/imports` | `{"repo", "path", "commit", "sha256", "name"?}`. Rereads the file at that commit, checks the hash, writes member and sidecar. 409 on a clash. |
| `GET  .../crew/sources/{owner}/{repo}` | The source's member list (name, path, blob, imported state). |
| `PUT  .../crew/sources` | `{"sources": [...]}`, the whole list. |
| `POST .../crew/members/{name}/update/preview` | The current bytes, the new bytes, the new commit and tag, and the checks' result. |
| `POST .../crew/members/{name}/update` | `{"commit", "sha256"}` from the preview. Writes both files. |

`GET .../crew` gains, on each member view, a `shared` object when an attached or detached sidecar is present: `{"owner", "repo", "path", "commit", "tag", "detached", "update": "none" | "available" | "removed" | "unknown"}`.

Rename and Delete change as well: Rename moves `<name>.shared.json` with the member (the sidecar's `upstream_name` stays), and Delete removes it. Both refuse when the sidecar's target name is taken by some other file.

## Crew page

- **Detail header.** A shared member shows "shared by `<owner>`" under its name, linked to the file on GitHub at the pinned commit, and the tag or short SHA. It shows "Update available", "Removed upstream" or "Edited here" when they apply.
- **Actions.** A fleet member that isn't an attached import gets **Share** beside Edit, Rename and Delete, when sharing is on for the fleet. With sharing off, Share isn't shown; the Import dialog says sharing is off only for publishing. An attached import gets **Update** when one is available.
- **List toolbar.** **Import** beside "Add crew" opens the Import dialog: a box for a ref or URL, and the fleet's sources, each with Browse and Remove, and Add source.
- **Share flow.** With no share repo, Share first opens "Create your share repo": the owner (the `gh` login, read only), the name (`admiral-crew`, editable), a description, and the README it will write, then Create. Then the preview: the target (`<owner>/<repo>/crew/<name>.md`, and "Replaces the published version" when it does), the exact bytes in a monospace block, and Publish. A refusal shows in place of Publish.
- **Diff.** Update and "Replaces the published version" show a line diff. There is no diff component yet; the build adds a small one in `web/src/app/` (an LCS line diff, removed and added lines marked in text as well as colour).
- Member text from GitHub is shown with `renderMarkdown` (`web/src/app/markdown.ts`) for the card and as plain text for the raw file, never as HTML.

## Errors

| Case | What the engineer sees |
| --- | --- |
| Sharing off, Share called | 403 "Crew sharing is off for this fleet. Turn it on in Preferences." |
| No GitHub source, or not github.com | 409 "Connect GitHub (github.com) for this fleet first." |
| `gh` not signed in or token refused | 502 with the step and `gh`'s message; the GitHub page's reconnect advice. |
| Share repo private, archived, gone or not pushable | 409 naming the repo and the reason, with "Choose another repo". |
| Repo name taken at create | 409, offering to use it when it's public and pushable. |
| Privacy check hit | 422 naming the check, the line and the rule or code point. |
| Member changed since preview | 409; the page reloads the preview. |
| Published file changed upstream since preview (`sha` mismatch) | 409 from GitHub; the page reloads the preview. |
| Bad ref or URL | 400 naming the accepted forms. |
| Ref, repo or file not found | 404 naming which. |
| File too big, not UTF-8, hidden characters, fails `CheckFile`, has ad hoc fields | 422 naming the rule and line. |
| Name clash | 409 with the suggested name. |
| Upstream changed between preview and import or update | 409; the page reloads the preview. |
| GitHub rate limit | 429 with the reset time; the update check waits for its next turn. |
| Sidecar unreadable | The member shows as a plain fleet member with a warning. |

## Acceptance criteria

- **AC1.** A fleet's `crew_sharing.enabled` is false until the engineer turns it on in Preferences; while it is false, `POST .../crew/members/{name}/share` and `.../share/preview` answer 403 and the Crew page shows no Share action.
- **AC2.** With no `share_repo`, the first Share offers to create a public repo under the `gh` login named `admiral-crew` (editable); Create makes the repo through `POST /user/repos` with `private: false`, commits a README naming that owner and repo, and saves `share_repo`. Nothing in Admiral names a particular user.
- **AC3.** Share shows the exact bytes, target path and sha256 before anything is sent; Publish commits those bytes, byte for byte, to `crew/<name>.md` in the share repo through `gh api` with the body on stdin, and returns the commit URL.
- **AC4.** The published bytes are the canonical member file: unknown fields, `ad-hoc` and `ad-hoc-run` are absent, and no memory note, ad hoc member or run member is sent.
- **AC5.** Share is refused (422, no GitHub write) when the bytes contain a `decoy-secret-<hex>` value, a hidden character such as U+202E or U+E0041, or an absolute path under `$ADMIRAL_HOME`, the home directory, the clone root or a search root; the message names the line and the rule, never the secret.
- **AC6.** Publishing when the member changed after the preview answers 409 and commits nothing.
- **AC7.** Share is not offered on built-in members or on an imported member that is still attached.
- **AC8.** Pasting `<owner>/<repo>/<name>`, `<owner>/<repo>/<name>@<ref>` or a GitHub `blob` URL previews the full file, the rendered card, "shared by `<owner>`", the pinned commit SHA and, when a tag points at that commit, the tag; nothing is written until Import is clicked.
- **AC9.** Import writes `crew/<name>.md` in the chosen fleet's crew directory and `crew/<name>.shared.json` with `version`, `host`, `repo`, `path`, `upstream_name`, `commit`, `upstream_blob`, `content_sha256` and the times; the member resolves exactly as a hand-written fleet member would, and the sidecar is never a member in either resolver.
- **AC10.** A file that fails `crew.CheckFile`, holds a hidden character, is over 64 KiB, is not UTF-8 or carries `ad-hoc` or `ad-hoc-run` is refused with the rule and line, and nothing is written.
- **AC11.** Importing a name a fleet member or built-in already has never overwrites: it answers 409 with `<name>-<owner>` suggested, and accepting it writes both the `name` field and the file name as the new name.
- **AC12.** Skills the member names that the dock can't see are listed as warnings before and after import; no skill is installed.
- **AC13.** Adding `<owner>/<repo>` (or its GitHub URL) as a crew source saves it in `crew_sharing.sources` and lists its `crew/*.md` members from one contents call, ignoring other files; Import from the list works as AC8 to AC12.
- **AC14.** Opening the Crew page, and a daily background check, make one GitHub API call per source repo and show "Update available" on an attached shared member whose upstream blob differs from its sidecar's, and "Removed upstream" when the file is gone.
- **AC15.** Update shows a line diff of the current and new file and writes nothing until Apply; Apply writes the new file and moves the sidecar's pin to the new commit.
- **AC16.** A shared member whose file no longer matches `content_sha256` is shown as "Edited here" with no update badge, and editing a shared member on the Crew page warns first that it detaches.
- **AC17.** Rename moves the sidecar with the member and Delete removes it; no orphan sidecar is left.
- **AC18.** AdmiralBot (and any non-engineer actor) gets 403 on every new route.
- **AC19.** A fleet without a github.com GitHub source gets "Connect GitHub (github.com) for this fleet first." on Share and Import, and no `gh` call is made.

## Open questions

- **GitHub Enterprise.** The first build is github.com only. Should a GHE fleet share to its own host, and import from github.com through a second account?
- **Does the switch gate import?** As written it gates publishing only; import and sources are always available. A work fleet may want import off too.
- **Tags.** Updates follow the default branch, even for a member imported at a tag. Should an import at a tag follow newer tags instead?
- **Sharing a detached import.** Share is offered only on members without an attached sidecar, so a detached import can be shared (it's the engineer's edit now). Should it be, and should the README credit the original?
- **Secret rules.** A vendored subset in Go, or run the `gitleaks` binary when installed? The subset keeps Admiral self-contained and tests fast; it will miss rules gitleaks has.
- **Commodore and provenance.** Should Commodore's `crew.py` show "shared by" too, which would make the sidecar part of the member format in `jbuffin/skills`?
- **Unpublish.** Should Admiral offer to delete a member from the share repo, or leave that to GitHub?

## Conflicts with today's code

- `textsafe.Hidden` treats `\n` and `\t` as hidden (they are `unicode.IsControl`) and misses the Unicode tag block (U+E0000 to U+E007F) and U+2060. The whole-file check combines it with crew notes' category set and exempts `\n` and `\t`.
- There is no secret scanner in Go, and `.gitleaks.toml` isn't run in CI. `internal/secretscan` is new.
- Rename and Delete in `internal/crewapi` move and remove only `<name>.md`; they must carry the sidecar.
- [ADR 0010](adr/0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md) says fleets are edited through the setup terminal; `PUT /api/fleets/{key}` already amends that for other fields, and `crew_sharing` joins them.
- The member format is Commodore's. If `jbuffin/skills` ever makes the resolver read `*.json` or files with two dots, the sidecar breaks; the sidecar fixture upstream guards that.

## Future work

- Sharing missions (`crew/missions/`), with the same repo and checks.
- A search across sources.

## Suggested build order

Each unit is one PR and builds only on the ones before it.

1. **Sharing groundwork.** `crew_sharing` on the fleet (config, `PUT /api/fleets/{key}`, the Preferences toggle); the sidecar type with read, write and detached check; Rename and Delete carrying the sidecar; the sidecar fixture from `jbuffin/skills`; `internal/secretscan` and the whole-file hidden-character and path checks as one `publish check` function with unit tests. AC1 (API part), AC17, the check halves of AC5 and AC10.
2. **Publish.** `GET .../crew/sharing`, `POST .../crew/sharing/repo`, share preview and share, through `sources.FleetGitHub`; the Share button, the create-repo dialog and the preview on the Crew page; fake `gh` e2e. AC1 to AC7, AC18 and AC19 for these routes.
3. **Import by ref.** Ref and URL parsing, import preview and import, clash rename, skill warnings, "shared by" on the detail header and `shared` on the member view; the Import dialog's ref box. AC8 to AC12, AC18 and AC19 for these routes.
4. **Sources and browse.** `crew_sharing.sources`, the sources routes, Add, Browse and Remove in the Import dialog. AC13.
5. **Pinned updates.** The checker (page open and daily, one call per repo), update badges, update preview with the line diff component, Apply, and the detach warning on Edit. AC14 to AC16.
