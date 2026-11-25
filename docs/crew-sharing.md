# Crew sharing

Status: agreed by the engineer on 2026-11-24 ([ADR 0049](adr/0049-crew-members-are-shared-as-files-in-a-public-repo-the-engineer-owns.md), accepted).

How an engineer publishes a crew member to a public GitHub repo, imports one someone else published, keeps it up to date, and takes a published member back down. The decision and its trade-offs are in [ADR 0049](adr/0049-crew-members-are-shared-as-files-in-a-public-repo-the-engineer-owns.md); this file is the build spec. Read it before changing crew sharing, the crew directory's file rules, or the Crew page's Share, Import and Unpublish flows.

The member file format belongs to Commodore (`jbuffin/skills`, `skills/engineering/commodore/references/crew-members.md`). Admiral publishes and imports that format as it is and adds nothing to it. Provenance lives in a separate file, the sidecar, whose schema Commodore also owns (see [Commodore and the sidecar](#commodore-and-the-sidecar)). Sharing missions is out of scope (see [Future work](#future-work)).

## Words

The terms (Share repo, Crew source, Shared member, Sidecar, Detached, Make mine, Unpublish) are defined in [GLOSSARY.md](../GLOSSARY.md); this file uses them as defined there.

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
- Admiral writes only `README.md` (at creation) and `crew/<name>.md` (on Share), and deletes only `crew/<name>.md` (on Unpublish, after a confirm).
- The scaffold README is templated from `<owner>/<repo>`: it names the repo, says it holds Admiral crew members (each file in `crew/` is one, for Commodore and Admiral), and gives the import ref forms below with this repo filled in. The Create dialog shows it before anything is written (`POST .../crew/sharing/repo` with `"preview": true`). Nothing in it is fixed to a particular user.

## Refs

A ref names one member in a crew source:

```text
<owner>/<repo>/<name>             # the default branch's head
<owner>/<repo>/<name>@<ref>       # a branch, a tag or a commit SHA
```

The import box also takes these GitHub URLs:

- `https://github.com/<owner>/<repo>/blob/<ref>/crew/<name>.md`: one member at `<ref>`.
- `https://github.com/<owner>/<repo>/tree/<ref>/crew` and `https://github.com/<owner>/<repo>`: no member named, so the box offers to add the repo as a crew source and opens its browse list.

`<owner>` and `<repo>` follow GitHub's rules. The build adds one owner/repo segment validator in `internal/github` (pattern `[A-Za-z0-9_.-]+`, as in `internal/campaigns/follow/routes.go:542`) and uses it for every GitHub endpoint segment. `<name>` must pass `crew.ValidName`. A `.md` suffix on `<name>` is dropped. Anything else is a 400 that names the form, for example `octocat/admiral-crew/reviewer@v1`.

Known limit: a ref is read with the charset `[A-Za-z0-9_./-]` (plus `%2F` for a `/` when it goes to GitHub), so a branch whose name holds another character can't be imported by name. Import it by commit SHA instead.

An import always resolves the ref to a commit SHA and pins that. **A tag is only a label**: when a tag in the repo points at the pinned commit, the Crew page shows it (`v1`) and the sidecar keeps it, but nothing follows tags. An import at `@v1` gets the same updates as one at the default branch (see [Updates](#updates)).

## The sidecar

`$ADMIRAL_HOME/fleets/<key>/crew/<name>.shared.json`, written beside `<name>.md` when a member is imported and rewritten when it is updated. Mode 0600, written atomically through a dot temp file like the member file. Its schema is defined in Commodore's `crew-members.md` (see [Commodore and the sidecar](#commodore-and-the-sidecar)); Admiral writes it to that schema.

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
- `upstream_blob` is the git blob SHA of `path` at `commit`. The update check compares it with the blob SHA the contents listing returns.
- `content_sha256` is the hash of the bytes Admiral wrote, after any clash rename. When `<name>.md` no longer hashes to it, the member is detached. Detached is computed on each read, never stored, so undoing a local edit by hand re-attaches it.
- "shared by `<owner>`" comes from `repo`'s owner part.
- Unknown fields are kept on rewrite. A sidecar that doesn't parse, has another `version`, or names a `path` outside `crew/` is ignored with a warning (`shared-unreadable`), and the member is shown as an ordinary fleet member; Admiral never deletes the sidecar on its own. Share stays refused while any sidecar file is there, readable or not; Make mine removes it.

**Resolution ignores the sidecar as a member.** Both resolvers read only regular `*.md` files that don't start with a dot (`memberFiles` in `internal/crew/resolve.go`; `crew.py`'s `member_files`), so `reviewer.shared.json` is never a member. They read it only as the member's provenance (below).

A sidecar with no `<name>.md` beside it is an orphan. The Crew page doesn't show it; Delete and Rename never leave one behind.

## Commodore and the sidecar

Commodore owns the member format, so it owns the sidecar schema too, and shows provenance in its own output in this build.

- **Where the schema lives.** A new "Shared members" section in `jbuffin/skills`'s `skills/engineering/commodore/references/crew-members.md`: the file name (`<stem>.shared.json` beside a member file, in any standing layer), every field above, which fields a resolver reads (`version`, `repo`, `commit`, `tag`, `content_sha256`), the validity rules every resolver applies (`host` is `github.com`, `path` is `crew/<file>.md`, the sidecar is strict UTF-8 JSON, and every readable member file's sidecar is checked), the detached rule, and that a bad sidecar is one warning, never an error. Admiral's Go resolver follows that text. Admiral's docs link to it rather than restating it.
- **What `crew.py` shows.**
  - `resolve` gives each member a `shared` field: `null`, or `{"owner", "repo", "commit", "tag", "detached"}` (`tag` may be null). `detached` is `sha256(raw bytes) != content_sha256`, computed from the bytes `read_member` already reads, so it costs one hash. A sidecar that can't be read or fails the schema gives `shared: null` and a `shared-unreadable` warning for that file.
  - `cast`'s Source column reads `fleet (shared by <owner>/<repo>@<short sha>)`, with `, edited here` added when detached. A pinned run-layer member keeps `(pinned)` as today.
  - `notes` and `adhoc` don't change. The ad hoc directory never has sidecars (it's skipped there).
- **How the two stay in step.** The shared fixtures in `jbuffin/skills`'s `fixtures/crew-resolution/` gain cases `shared-attached`, `shared-detached`, `shared-tag-null`, `shared-bad-sidecar` and `shared-orphan` (a sidecar with no member: no member, no warning), and every existing `expected.json` gains `"shared": null` on each member. Admiral vendors them with `sync.sh` into `internal/crew/testdata/crew-resolution`, and its Go resolver returns the same `shared` object, so `GET .../crew` builds its member view from the resolver rather than from its own sidecar reader. The `jbuffin/skills` change merges first; Admiral's resolver change lands with the re-vendored fixtures.
- **Writing stays Admiral's.** Commodore reads sidecars and never writes one.

## Fleet settings

A new object on the fleet in `config.json`, edited through `PUT /api/fleets/{key}` like the other fleet fields (`config.Manager.SetFleetFields`) and returned on the fleet view:

```json
"crew_sharing": {
  "enabled": false,
  "share_repo": "octocat/admiral-crew",
  "sources": ["octocat/admiral-crew", "hubot/crew"]
}
```

- `enabled` gates **publishing only**: creating the share repo and Share. It is false when the object is missing. Turning it on is a toggle on the fleet's Preferences page, with one sentence under it: "Lets you publish this fleet's crew members to a public GitHub repo."
- Import, crew sources, update checks, Update, Make mine and Unpublish work whatever `enabled` says. Each write is still the engineer's click.
- `share_repo` is set when the scaffold creates the repo or the engineer picks an existing one on first Share. It can be cleared or changed in Preferences.
- `sources` is the list of crew source repos, in the order added, without duplicates. Adding a source from the Crew page writes it here.
- Invalid values are a 400 naming the field, the way the other fleet fields are.
- Nothing about the secret scan is stored (see [Privacy checks](#privacy-checks)).

## GitHub access

Every call goes through the fleet's built-in GitHub source client, `sources.FleetGitHub(cfg, fleet)` in `internal/sources/github.go`, which runs `gh api` with the fleet's account and env (`internal/github.Client.rest`). No new token store, no second `gh` account, no HTTP client of Admiral's own.

- A fleet with no GitHub source can't share or import: the Crew page says "Connect GitHub (github.com) for this fleet first." and links to the fleet's GitHub page.
- **github.com only.** A fleet whose GitHub source is on another host gets the same refusal, and no `gh` call is made. GitHub Enterprise is later work.
- Reading a public crew source uses the fleet's token too, which keeps the rate limit at 5,000 calls an hour.

The calls, all with `gh api --method <M> --hostname github.com [--input -] <endpoint>`:

| Step | Call |
| --- | --- |
| Who am I | `GET /user` (login, for the scaffold owner) |
| Check share repo | `GET /repos/{owner}/{repo}` (`visibility` must be `public`, `permissions.push` true, `default_branch`) |
| Create share repo | `POST /user/repos` `{"name", "description", "private": false, "auto_init": false}` |
| Write README | `PUT /repos/{owner}/{repo}/contents/README.md` `{"message", "content"}` (the repo's first commit) |
| Publish a member | `PUT /repos/{owner}/{repo}/contents/crew/{name}.md` `{"message", "content", "sha"?}` (`sha` is the current blob when replacing; absent when new) |
| Unpublish a member | `DELETE /repos/{owner}/{repo}/contents/crew/{name}.md` `{"message", "sha"}` |
| Resolve a ref | `GET /repos/{owner}/{repo}/commits/{ref}` (the commit SHA) |
| Read a member | `GET /repos/{owner}/{repo}/contents/crew/{name}.md?ref={sha}` |
| Find a tag | `GET /repos/{owner}/{repo}/tags?per_page=100` (first tag whose commit is the pin) |
| List a repo's `crew/` | `GET /repos/{owner}/{repo}/contents/crew` (each file's name and blob `sha`, on the default branch) |

Bodies go on stdin, never `-F`, so the fake `gh` (`e2e/testdata/fakegh/gh`) serves every call. Each step's failure text names the step and the repo, with `gh`'s message after it.

The scaffold creates the repo only under the `gh` account itself (`/user/repos`), never under an organization, and only on the engineer's click, like [ADR 0047](adr/0047-admiral-adds-or-creates-a-fleets-project-through-one-route.md). A name already taken is a 409 that offers "Use the existing repo" when the account can push to it and it is public.

## Privacy checks

A publish sends exactly one file. Admiral builds its bytes and checks them for the preview, and checks them again on commit.

1. **Only the engineer's own fleet members.** Share is offered on a member whose layer is `fleet` and that has no sidecar file at all, attached, detached or unreadable. Imports are never re-shared: an imported member shows no Share action and no hint pointing at one. Make mine, its own action with its own confirm, is the way to make an import the engineer's own. A built-in has no file to share (customize it first). Memory notes, ad hoc members and run members are never offered.
2. **Canonical bytes.** Admiral rebuilds the file from the resolved member with `crewapi`'s canonical writer (`memberBody.file`), not the file on disk. That drops unknown fields and never writes `ad-hoc` or `ad-hoc-run`. The bytes pass `crew.CheckFile` first.
3. **No paths from the Mac.** The bytes are refused when they contain `$ADMIRAL_HOME`, the user's home directory, the fleet's clone root or any of its search roots, as absolute paths. The refusal names the line.
4. **No hidden characters.** The whole file goes through `crewapi`'s existing `hidden(s, true)` (`internal/crewapi/draft.go:118`) together with the `textsafe.Hidden` and Unicode tag-block rules, and crew notes' hidden categories (Cc, Cf, Cs, Co, Cn, Zl, Zp). Any rune one of them flags refuses the publish, except `\n` and `\t`. That includes variation selectors (U+FE00 to U+FE0F, U+E0100 to U+E01EF) and blank fillers (U+115F, U+1160, U+3164, U+FFA0, U+2800), which draw nothing and can smuggle text. The refusal names the line and the code point (`U+202E on line 7`).
5. **No `gitleaks:allow`.** Bytes containing `gitleaks:allow` are refused, naming the line, because gitleaks honours that inline comment and would report a secret on that line as clean.
6. **Secret scan with gitleaks.** See below.

Checks 1 to 5 always run and have no override. The engineer fixes the member (Edit on the Crew page) and tries again.

### The secret scan

Admiral runs the engineer's own `gitleaks` on the exact bytes. It has no scanner of its own.

- **Which gitleaks.** Admiral finds `gitleaks` on the fleet env's `PATH` and reads `gitleaks version` (a leading `v` is dropped). 8.19.0 or later is usable. Older is **too old**: it has no `stdin` command, and Admiral treats it like a missing gitleaks. There is no `gitleaks dir` fallback. A version it can't read is a failed scan.
- **How it runs.** Admiral runs `gitleaks stdin --no-banner --redact --log-level error --report-format json --report-path - --exit-code 3`, with the bytes on stdin, nothing written to disk, the working directory a fresh empty temp directory, and `GITLEAKS_CONFIG` and `GITLEAKS_CONFIG_TOML` removed from its env, so gitleaks uses its default rules and never a repo's allowlist. A 30-second timeout.
- **Reading the result.** Exit 0: clean. Exit 3: a hit; Admiral reads `RuleID` and `StartLine` from the JSON report (`--redact` keeps the secret out of it). Any other exit, a crash, a timeout or unparsable output: the scan **failed**.
- **A hit refuses the publish**, naming the line and the rule, never the text. There is no opt-out on a hit.
- **A failed scan refuses the publish** with gitleaks' error (its first stderr line, or "timed out after 30s"). There is no opt-out: gitleaks is there, so the engineer fixes it or tries again.
- **Missing or too old.** When `gitleaks` isn't found, the preview says "Secret scan unavailable: gitleaks isn't installed" and shows how to install it (`brew install gitleaks`, with a link to gitleaks' install docs). When it's too old, it says "Secret scan unavailable: gitleaks `<version>` is too old (8.19 or later needed)" and shows the upgrade command (`brew upgrade gitleaks`). Both offer **Publish without a secret scan**. Admiral never installs or upgrades it, the way it installs nothing but Commodore ([ADR 0040](adr/0040-admiral-installs-commodore-as-a-harness-plugin.md)).
- **The opt-out is per publish.** Choosing it shows, in the preview: "This file goes public without a secret scan. Read it for keys, tokens and passwords before you publish." The publish request carries `"skip_secret_scan": true`; nothing is saved, so the next publish scans again or asks again. The opt-out exists only for a missing or too-old gitleaks: the server rejects `skip_secret_scan` (400) when a usable gitleaks is installed, whether its scan was clean, hit or failed.
- **Tests.** A fake `gitleaks` in `e2e/testdata/` (beside the fake `gh`) reports a hit on any `decoy-secret-<hex>` value, with its line, and is clean otherwise; its version and failures (a crash, an error exit, a hang) are set by the test. A test without it on `PATH` covers the missing case, and one with it reporting `8.18.0` the too-old case. Unit tests drive the runner through a seam. No key-shaped string enters the tree.
- **On import** the scan runs the same way and a hit is shown as a warning, not a refusal (a publisher's secret is their problem, but the engineer should know). Missing gitleaks on import shows nothing.

**Preview equals commit.** The preview route returns the bytes, their sha256 and the scan state. The publish route takes that sha256 (and `skip_secret_scan` when chosen), rebuilds the bytes, runs every check again, and commits only when the hash still matches; otherwise it's a 409 "The member changed since the preview" and the page shows the new preview.

## Import

Importing is reading a file from GitHub and writing it as a fleet member, with these rules.

- **Read the whole file first.** The import dialog shows the member's rendered card (name, role, based-on, skills, model, harness, pick-me-when) and the full raw file, with "shared by `<owner>`", the repo, the pinned short SHA and the tag. Nothing is written until the engineer clicks Import.
- **Refuse a bad file.** The bytes must be valid UTF-8, at most `crew`'s 64 KiB limit, free of hidden characters (the same whole-file check as publish check 4), and pass `crew.CheckFile` with the stem from the file name. A file with `ad-hoc` or `ad-hoc-run` is refused: those belong to a run, not a fleet. Other frontmatter keys the resolver doesn't know are kept as they are, so a file written for a newer Admiral round-trips. Each refusal says which rule and which line.
- **Never overwrite.** When the name is taken (a fleet member file exists, or a built-in has it), the dialog says so and offers `<name>-<owner>` (cut to the 64-character slug limit; when that is taken too, the engineer types one). Renaming rewrites the `name` field (`renamed` in `crewapi`) and the file name together, then runs `crew.CheckFile` again. The write uses a hard link so a file that appeared since the check is never replaced (`KeepMission`'s pattern).
- **Skills are warnings.** Each skill the member names that the fleet's dock can't see (`GET /api/fleets/{key}/crew/skills`) is listed: "Not installed: `<skill>`". Import is still allowed; Admiral never installs a skill.

The member file and sidecar are written under the crew write lock; the member first, then the sidecar. A failure between them leaves a plain fleet member, which the Crew page shows as one.

## Crew sources and browsing

- **Add a source** from the Crew page's Import dialog (a repo URL or `<owner>/<repo>`). Admiral checks the repo is reachable and has a `crew/` folder, then appends it to `crew_sharing.sources`.
- **Browse** lists a source's members from one contents listing call, then reads each file only when the engineer opens it. Each row shows the name and, once read, the role. A member already imported from this source and path shows "Imported" (and its update state) instead of Import.
- **Remove a source** removes it from the list. Members imported from it stay, still pinned, and still get update checks (the checker covers every repo a sidecar names).

## Updates

**What counts.** An update is any change to the member's file on the source repo's default branch, whatever the import pinned and whatever tags say.

**When.** Admiral checks when the Crew page opens and once a day in the background.

- The background check is a loop modeled on `internal/update.Checker`: started from `serve()`, the first check after a delay read by `internal/delay.From` (`ADMIRAL_CREW_UPDATE_FIRST_CHECK`, so tests can make it quick), then every 24 hours, with an injected clock. Results are kept in memory per fleet, not on disk.
- Opening the Crew page asks the checker for the fleet's state. A result younger than five minutes is reused; otherwise it checks now and the page shows "Checking for updates" on shared members until it answers. A failed check shows nothing new and logs the error; the next one retries.

**How.** One call per repo that any attached sidecar in the fleet names: `GET /repos/{owner}/{repo}/contents/crew` on the default branch. For each attached shared member from that repo, the listed blob SHA of its `path` is compared with its sidecar's `upstream_blob`:

- Same: up to date.
- Different: "Update available".
- Missing from the listing: "Removed upstream". The member stays as it is.
- Detached: no check and no badge; the detail says "Edited here, so it no longer gets updates from `<repo>`."

The badge shows on the member's list row and on the detail header.

**Update.** Clicking Update resolves the default branch to a commit SHA, reads the file there, and shows a line diff of the current file against the new one, with the tag that points at the new commit if any, and the same checks as an import (a file that fails one can't be applied). Apply writes the new bytes (with the local name kept, through `renamed` if it differs from upstream) and rewrites the sidecar's `commit`, `tag`, `upstream_blob`, `content_sha256` and `updated_at`. Nothing updates without that click.

**Detach.** Editing a shared member on the Crew page first warns "Editing detaches this member from `<repo>`. It won't get updates." A hand edit on disk detaches it silently, and the Crew page says so the next time it loads.

## Make mine

**Make mine** on a shared member (attached, detached or with an unreadable sidecar) asks "Stop tracking `<repo>`? This member will no longer get updates or show who shared it." and, on confirm, deletes `<name>.shared.json` and leaves `<name>.md` as it is. The member is then an ordinary fleet member: no badge, no "shared by", no update checks, and Share is offered (when sharing is on). It's the only way an imported member becomes shareable, and nothing else offers it: Share is never shown on an imported member.

## Unpublish

**How Admiral knows what you published.** It lists the share repo: a member is published when `crew/<name>.md` is in the `GET .../contents/crew` listing of the fleet's `share_repo`. Admiral keeps no local record of publishes, so a file published from another Mac, renamed on GitHub or deleted there shows correctly. The listing is fetched with the update check (one more call when the share repo isn't already a source) and when the Crew page opens, under the same five-minute reuse.

- A fleet member whose name is in the listing shows "Published" and an **Unpublish** action.
- The Import dialog's share repo row (the share repo is always listed there, as "Your share repo") browses it like a source, with Unpublish on every row, so a file whose local member was deleted or renamed can still be removed.
- Unpublish reads the file's current bytes, blob SHA and repo, and the confirm shows the repo, the path and those bytes: "Delete `crew/<name>.md` from `<owner>/<repo>`? People who imported it keep their copy; they'll see it as removed upstream. It stays in the repo's history." Confirm sends the `DELETE` with `{"blob", "repo"}` as the GET gave them. A changed file, or a share repo that is no longer the one the confirm read, is a 409 and the confirm reloads. A file that is already gone is a 404 and the Crew page refreshes.
- Unpublish doesn't need the sharing switch on (it only removes), and never touches the local member.
- Git history keeps the file. The confirm says so: "It stays in the repo's history."

## API

All under `/api/fleets/{key}/crew/`, JSON in and out, the `?harness=` dock query where a member list is involved, like the existing crew routes. None is added to the actor guard's allowlist, so AdmiralBot, routines and ships get 403.

| Route | What |
| --- | --- |
| `GET  .../crew` | Each member also carries `shared_url`: `https://github.com/<owner>/<repo>/blob/<commit>/crew/<file>.md` for an imported member (built on the server from its sidecar, each segment escaped), else null. |
| `GET  .../crew/sharing` | `{"crew_sharing": {"enabled", "share_repo", "sources"}, "connected", "login", "login_error", "repo", "published": [...], "published_error"}`: the fleet's `crew_sharing` state, whether GitHub is connected, the `gh` login, whether the share repo exists and is pushable, its published names, and why the listing failed when it did (`published_error`). |
| `POST .../crew/sharing/repo` | Create the share repo and its README `{"name", "description"}`, or adopt an existing one `{"repo": "owner/name"}`. `{"preview": true, "owner", "name"}` writes nothing and answers `{"readme"}`, the README the create would commit. Saves `share_repo` and answers `{"share_repo"}`. 403 when sharing is off. A taken name is a 409; when the repo is public and pushable it carries `"use_existing": "owner/name"`. |
| `POST .../crew/members/{name}/share/preview` | No body: the fleet's saved share repo is the target. The canonical bytes (`bytes`), `sha256`, `path`, `repo`, `replaces_blob` (the published file's blob SHA, or null for a new file), `published_bytes` when it replaces one (read by the preview itself; 422 when the published file is over 64 KiB), and `scan`: `{"state": "clean" \| "hit" \| "failed" \| "missing" \| "too-old", "findings": [{"line", "rule"}], "error": "<text>", "version": "<found>", "install": "<hint>"}`. A failed check 1 to 5 is a 422 naming the check, the line and the rule. 403 when sharing is off. |
| `POST .../crew/members/{name}/share` | `{"sha256", "repo", "replaces_blob", "skip_secret_scan"?}`, the first three as the preview gave them. Rechecks and commits. Returns `{"name", "repo", "path", "commit_url"}`, where `commit_url` is `https://github.com/<repo>/commit/<sha>` built from the commit sha GitHub returned, or `""` when it returned none. The share repo is checked again first (409 when it moved, is private or archived, or is no longer pushable), then the hash, then the scan. 403 when sharing is off; 422 on a hit, with the `scan` in the body; 409 when the member, the share repo or the published file changed since the preview. |
| `POST .../crew/members/{name}/make-mine` | Deletes the sidecar. 404 when there's none. |
| `GET  .../crew/published/{name}` | `{"name", "repo", "bytes", "blob"}`: the published file's bytes and blob SHA, for the Unpublish confirm. 404 when it isn't published; 422 when it is over 64 KiB. |
| `DELETE .../crew/published/{name}` | `{"blob", "repo"}` from the GET. Deletes `crew/<name>.md` from the share repo and returns `{"name", "repo", "commit_url"}`. 409 when the file changed since the GET or `repo` is no longer the fleet's share repo; 404 when the file is gone. |
| `POST .../crew/imports/preview` | `{"ref"}` (a ref or URL). Returns `bytes` (the raw file), `card` (the parsed member), `owner`, `repo`, `path`, `commit`, `tag`, `blob`, `sha256`, `clash` (`{"name", "suggested"}`, or null), `missing_skills` (skills no dock of the fleet has) and `scan` (as in the Share preview; a failed scan is a warning here and never blocks). A URL with no member returns `{"source": "owner/repo"}` instead. 409 when the fleet has no github.com client; 404 naming the ref, file or repo that wasn't found; 403 for a bot. |
| `POST .../crew/imports` | `{"repo", "path", "commit", "sha256", "name"?}`. Rereads the file at that commit, checks the hash (409 "The file changed since the preview. Preview it again."), rechecks the file, then writes the member and its sidecar without replacing anything. Returns `{"name", "missing_skills", "scan"}`. 409 on a clash, with a top-level `suggested`. |
| `GET  .../crew/sources/{owner}/{repo}` | `{"members": [{"name", "path", "blob", "imported", "published"}]}`: `imported` is the fleet member's name when one came from that file, else null; `published` is true for a file in the fleet's share repo. |
| `PUT  .../crew/sources` | `{"sources": [...]}`, the whole list. Only entries new to the list are checked (the repo exists and has a `crew/` folder; 404 otherwise). Returns `{"sources": [...]}`. |
| `POST .../crew/members/{name}/update/preview` | The current bytes, the new bytes, the new commit and tag, and the checks' result. |
| `POST .../crew/members/{name}/update` | `{"commit", "sha256"}` from the preview. Writes both files. |

`GET .../crew` gains, on each member view, the resolver's `shared` object (`{"owner", "repo", "commit", "tag", "detached"}` or null) plus Admiral's `update` (`"none"`, `"available"`, `"removed"` or `"unknown"`), `published` (bool, from the share repo listing) and `sidecar_unreadable` (bool).

Rename and Delete change as well: Rename moves `<name>.shared.json` with the member (the sidecar's `upstream_name` stays), and Delete removes it. Both refuse when the sidecar's target name is taken by some other file.

## Crew page

- **Detail header.** A shared member shows "shared by `<owner>`" under its name, linked to the file on GitHub at the pinned commit, and the tag or short SHA. It shows "Update available", "Removed upstream" or "Edited here" when they apply. A published member shows "Published", linked to the file in the share repo. When the published listing failed, the page says so (`published_error`) instead of showing the marks as if none existed.
- **Actions.** A fleet member with no sidecar gets **Share** beside Edit, Rename and Delete, when sharing is on for the fleet and GitHub is connected; with GitHub not connected the page shows "Connect GitHub" linking to the fleet's GitHub page instead. Built-in members never show Share. A shared member gets **Make mine**, and **Update** when one is available, and never Share. A published member gets **Unpublish**.
- **List toolbar.** **Import** beside "Add crew" opens the Import dialog: a box for a ref or URL, "Your share repo" when there is one, and the fleet's sources, each with Browse (and Remove for sources), and Add source.
- **Share flow.** With no share repo, Share first opens "Create your share repo": the owner (the `gh` login, read only), the name (`admiral-crew`, editable), a description, the README it will write, then Create; or "Use an existing repo", which takes `owner/name`. Then the preview: the target (`<owner>/<repo>/crew/<name>.md`, and "Replaces the published version" with a diff when it does), the exact bytes in a monospace block, the secret scan's state (always shown, clean or not), and Publish. A refusal shows in place of Publish. A share repo that can't be written shows its message with **Choose another repo**, which opens the repo step. A 409 on Publish (the member, the published file or the repo changed) shows its message and loads the preview again. Without gitleaks, or with one older than 8.19, the install or upgrade hint, a link to gitleaks' install guide and **Publish without a secret scan** with its warning show; Publish stays disabled until the box is ticked.
- **Diff.** Update and "Replaces the published version" show a line diff. There is no diff component yet; the build adds a small one in `web/src/app/` (an LCS line diff, removed and added lines marked in text as well as colour).
- Member text from GitHub is shown with `renderMarkdown` (`web/src/app/markdown.ts`) for the card and as plain text for the raw file, never as HTML.

## Errors

| Case | What the engineer sees |
| --- | --- |
| Sharing off, Share or repo create called | 403 "Crew sharing is off for this fleet. Turn it on in Preferences." |
| Share on a member with a sidecar | 409 "Imported members can't be shared. Make it yours first." |
| No GitHub source, or not github.com | 409 "Connect GitHub (github.com) for this fleet first." |
| `gh` not signed in or token refused | 502 with the step and `gh`'s message; the GitHub page's reconnect advice. |
| Share repo private, archived, gone or not pushable | 409 naming the repo and the reason, with "Choose another repo". |
| Repo name taken at create | 409, with `use_existing` (the page's "Use `owner/repo`") when it's public and pushable. |
| Share repo changed since the preview or the confirm | 409 "Preview it again." (Share) or "Reload and try again." (Unpublish) |
| Share repo moved or renamed on GitHub | 409 "The share repo X moved to Y. Choose another repo." |
| `sha256` missing or malformed, or `replaces_blob` malformed, from Share, or `blob` missing or malformed from Unpublish | 400 saying it must come from the preview or the GET. A missing or null `replaces_blob` means a new file. |
| Privacy check 1 to 5 hit | 422 naming the check, the line and the rule or code point. |
| gitleaks hit | 422 naming the line and the rule. No opt-out. |
| gitleaks installed but the scan crashed, errored or timed out | 422 with the error. No opt-out. |
| gitleaks missing or older than 8.19 | The preview's scan state, the install or upgrade hint, and the one-publish opt-out with its warning. |
| `skip_secret_scan` with a usable gitleaks installed | 400. |
| Member changed since preview | 409; the page reloads the preview. |
| Published file changed upstream since preview or confirm (`sha` mismatch) | 409 from GitHub; the page reloads the preview or confirm. |
| Unpublish of a file that isn't there | 404; the page refreshes the published state. |
| Bad ref or URL | 400 naming the accepted forms. |
| Ref, repo or file not found | 404 naming which. |
| File too big, not UTF-8, hidden characters, fails `CheckFile`, has ad hoc fields | 422 naming the rule and line. |
| Name clash | 409 with the suggested name. |
| Upstream changed between preview and import or update | 409; the page reloads the preview. |
| GitHub rate limit | 429 with the reset time when `gh` gives one; the update check waits for its next turn. |
| Sidecar unreadable | The member shows as a plain fleet member with a warning and Make mine. |

## Acceptance criteria

Publishing:

- **AC1.** A fleet's `crew_sharing.enabled` is false until the engineer turns it on in Preferences; while it is false, `POST .../crew/sharing/repo`, `.../share/preview` and `.../share` answer 403 and the Crew page shows no Share action.
- **AC2.** With `enabled` false, import preview, import, adding and browsing a source, the update check, update preview and Update, Make mine and Unpublish all still work, and each write happens only on the engineer's click.
- **AC3.** With no `share_repo`, the first Share offers to create a public repo under the `gh` login named `admiral-crew` (editable); Create makes the repo through `POST /user/repos` with `private: false`, commits a README naming that owner and repo, and saves `share_repo`. Nothing in Admiral names a particular user.
- **AC4.** Share shows the exact bytes, target path, sha256 and secret scan state before anything is sent; Publish commits those bytes, byte for byte, to `crew/<name>.md` in the share repo through `gh api` with the body on stdin, and returns the commit URL.
- **AC5.** The published bytes are the canonical member file: unknown fields, `ad-hoc` and `ad-hoc-run` are absent, and no memory note, ad hoc member or run member is sent.
- **AC6.** Share is refused (422, no GitHub write, no override) when the bytes contain a hidden character such as U+202E, U+E0041, a run of U+E0100 or U+3164, or an absolute path under `$ADMIRAL_HOME`, the home directory, the clone root or a search root; the message names the line and the rule.
- **AC7.** Publishing when the member changed after the preview answers 409 and commits nothing.
- **AC8.** Share is not shown, and the share routes answer 409, on a built-in member or on any member with a sidecar file, whether attached, detached or unreadable; an imported member's detail shows Make mine as its own action with its own confirm, and nothing on it offers Share or Make mine-and-share in one step.

Secret scan:

- **AC9.** With gitleaks on the fleet's `PATH`, preview and publish run `gitleaks stdin` on the exact bytes with nothing written to disk and no gitleaks config from the env or a repo; a test with the fake gitleaks shows a `decoy-secret-<hex>` value refuses the publish with 422 naming the line and the rule and never the value, and no opt-out is offered; bytes containing `gitleaks:allow` are refused with 422 naming the line, with no opt-out, before gitleaks runs.
- **AC10.** Without gitleaks on `PATH`, the preview says the secret scan is unavailable, shows how to install it, and offers Publish without a secret scan with its warning; publishing then needs `skip_secret_scan: true` in that request, and the next publish asks again. Admiral never runs an install.
- **AC11.** `skip_secret_scan: true` is refused with 400 when a usable gitleaks (8.19 or later) is installed, whatever its scan returned, and `config.json` never records a scan choice.
- **AC11a.** With a gitleaks older than 8.19 on `PATH` (the fake reporting `8.18.0`), the preview says it's too old, shows the upgrade command and offers the same one-publish opt-out with its warning as AC10; Admiral never runs `gitleaks dir` or any other fallback scan.
- **AC11b.** With a usable gitleaks whose scan crashes, exits with an error or passes the 30-second timeout, preview and publish refuse with 422 showing the error, no opt-out is offered, and nothing is committed.

Import:

- **AC12.** Pasting `<owner>/<repo>/<name>`, `<owner>/<repo>/<name>@<ref>` or a GitHub `blob` URL previews the full file, the rendered card, "shared by `<owner>`", the pinned commit SHA and, when a tag points at that commit, the tag; nothing is written until Import is clicked.
- **AC13.** Import writes `crew/<name>.md` in the chosen fleet's crew directory and `crew/<name>.shared.json` with `version`, `host`, `repo`, `path`, `upstream_name`, `commit`, `upstream_blob`, `content_sha256` and the times; the member resolves as a hand-written fleet member would plus its `shared` object, and the sidecar is never a member in either resolver.
- **AC14.** A file that fails `crew.CheckFile`, holds a hidden character, is over 64 KiB, is not UTF-8 or carries `ad-hoc` or `ad-hoc-run` is refused with the rule and line, and nothing is written; a run of U+E0100 and a U+3164 are each refused as hidden characters.
- **AC15.** Importing a name a fleet member or built-in already has never overwrites: it answers 409 with `<name>-<owner>` suggested, and accepting it writes both the `name` field and the file name as the new name.
- **AC16.** Skills the member names that the dock can't see are listed as warnings before and after import; no skill is installed.
- **AC17.** Adding `<owner>/<repo>` (or its GitHub URL) as a crew source saves it in `crew_sharing.sources` and lists its `crew/*.md` members from one contents call, ignoring other files; Import from the list works as AC12 to AC16.

Updates:

- **AC18.** Opening the Crew page, and a daily background check, make one GitHub API call per repo that the fleet's attached sidecars name, and show "Update available" on an attached shared member whose file's blob on the default branch differs from its sidecar's `upstream_blob`, and "Removed upstream" when the file is gone.
- **AC19.** A member imported at a tag shows "Update available" when its file changes on the default branch, even when no new tag exists; a new tag on an unchanged file shows no update.
- **AC20.** Update shows a line diff of the current and new file and writes nothing until Apply; Apply writes the new file and moves the sidecar's pin to the new commit.
- **AC21.** A shared member whose file no longer matches `content_sha256` is shown as "Edited here" with no update badge, and editing a shared member on the Crew page warns first that it detaches.

Make mine and Unpublish:

- **AC22.** Make mine, after its own confirm, deletes the member's sidecar and leaves its file byte for byte; the member then has `shared: null`, no badge and no update checks, and Share is offered when sharing is on.
- **AC23.** A fleet member whose name is in the share repo's `crew/` listing shows "Published" and Unpublish; a file published from elsewhere shows too, with no local record involved.
- **AC24.** Unpublish shows the repo, path and current bytes of the published file and deletes nothing until confirmed; Confirm deletes `crew/<name>.md` through `gh api` `DELETE` with that blob SHA, leaves the local member untouched, and a file changed since the confirm answers 409.

Housekeeping and access:

- **AC25.** Rename moves the sidecar with the member and Delete removes it; no orphan sidecar is left.
- **AC26.** AdmiralBot (and any non-engineer actor) gets 403 on every new route.
- **AC27.** A fleet without a github.com GitHub source gets "Connect GitHub (github.com) for this fleet first." on Share, Import and Unpublish, and no `gh` call is made.

Commodore (`jbuffin/skills`):

- **AC28.** Commodore's `crew-members.md` defines the sidecar: its file name and place, every field, the fields a resolver reads, the detached rule and the `shared-unreadable` warning.
- **AC29.** `crew.py resolve` gives every member `shared`: null without a sidecar, and `{"owner", "repo", "commit", "tag", "detached"}` with one, `detached` true when the file's sha256 differs from `content_sha256`; a bad sidecar gives `shared: null` and one `shared-unreadable` warning, never an error; a sidecar with no member is ignored.
- **AC30.** `crew.py cast`'s Source cell for a shared member reads `fleet (shared by <owner>/<repo>@<short sha>)`, with `, edited here` when detached.
- **AC31.** The fixture cases `shared-attached`, `shared-detached`, `shared-tag-null`, `shared-bad-sidecar` and `shared-orphan` pass in both `crew.py` and Admiral's Go resolver, and Admiral's vendored fixtures match `jbuffin/skills`.

## Open questions

None. The engineer answered the last ones on 2026-11-24.

## Conflicts with today's code

- `textsafe.Hidden` treats `\n` and `\t` as hidden (they are `unicode.IsControl`) and misses the Unicode tag block (U+E0000 to U+E007F), U+2060, variation selectors and blank fillers. The whole-file check combines it and the tag-block rule with `crewapi`'s `hidden(s, true)` (which refuses non-graphic runes, blank fillers and variation selectors that don't follow an emoji base) and crew notes' category set, and exempts `\n` and `\t`.
- `.gitleaks.toml` allowlists `decoy-(secret|helper)-[0-9a-f]+`, which is why the scan must not read a repo's config, and why tests use a fake gitleaks to prove a hit.
- Rename and Delete in `internal/crewapi` move and remove only `<name>.md`; they must carry the sidecar.
- [ADR 0010](adr/0010-fleets-are-set-up-by-admiral-setup-in-a-json-config.md) says fleets are edited through the setup terminal; `PUT /api/fleets/{key}` already amends that for other fields, and `crew_sharing` joins them.
- The member view's `shared` comes from the resolver, so the resolver's output (and every fixture's `expected.json`) changes shape. That change starts in `jbuffin/skills`.

## Future work

- GitHub Enterprise hosts.
- Sharing missions (`crew/missions/`), with the same repo and checks.
- A search across sources.

## Suggested build order

Each unit is one PR and builds only on the ones before it. Unit 1 is in `jbuffin/skills`, a separate repo and PR; the rest are in Admiral.

1. **Commodore reads sidecars (`jbuffin/skills`).** The "Shared members" section in `crew-members.md`, `shared` in `crew.py resolve`, the Source cell in `cast`, the five new fixture cases and `"shared": null` in every existing `expected.json`. AC28 to AC31 on Commodore's side.
2. **Groundwork (Admiral).** Re-vendor the fixtures and give the Go resolver the same `shared` object; `crew_sharing` on the fleet (config, `PUT /api/fleets/{key}`, the Preferences toggle); the sidecar writer; Rename and Delete carrying the sidecar; Make mine; the hidden-character, path and field checks as one publish check; the gitleaks runner with its fake. AC11, AC22, AC25, AC31 on Admiral's side, AC2 (Make mine part), AC26 and AC27 for make-mine, and the check halves of AC6 and AC9.
3. **Publish and Unpublish.** `GET .../crew/sharing`, `POST .../crew/sharing/repo`, share preview and share, the published listing, Unpublish; the Share button, the create-repo dialog, the preview with the scan state and opt-out, the Unpublish confirm, and the line diff component (for "Replaces the published version"); fake `gh` e2e. AC1, AC3 to AC11b, AC2 (Unpublish part), AC23, AC24, and AC26 and AC27 for these routes.
4. **Import and sources.** Ref and URL parsing, import preview and import, clash rename, skill warnings, "shared by" on the detail header; add, browse and remove sources. AC2 (import part), AC12 to AC17, and AC26 and AC27 for these routes.
5. **Pinned updates.** The checker (page open and daily, one call per repo), update badges, update preview with the line diff, Apply, and the detach warning on Edit. AC2 (update part), AC18 to AC21, and AC26 and AC27 for these routes.
