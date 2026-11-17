# Searching a fleet's issues for the Add campaign modal

> Research for the map "Add a campaign from Admiral by search, free text or a drafting skill", 2026-11-17. Question: which calls search a fleet's issues by text, `#n`, `owner/repo#n` and URL, how to tell a parent from a leaf, how fast and rate-limited they are, and what Admiral already fetches that the search can reuse.

## Answer in short

- **One GraphQL `search` call per keystroke burst covers text.** Use `search(type: ISSUE_ADVANCED)` with the fleet's scope written as `(repo:a/b OR repo:c/d OR user:o)`, and read `subIssuesSummary { total }` on each hit. It costs 1 GraphQL point, answers in about 0.8 s, and doesn't touch the REST search limit of 30 requests a minute. Admiral's GitHub client already runs this exact search type, with `subIssuesSummary`, for every order panel.
- **Parents first:** `has:sub-issue` (singular) filters to parents and `no:sub-issue` to leaves. Run the parents query and the full query in parallel, or run one query and stable-sort hits with `subIssuesSummary.total > 0` to the top. Both cost 1 point each.
- **`owner/repo#n` and URLs** already parse in `campaigns.ParseIssueRef`, and the server routes them to a fleet. Resolve one with a single `repository { issueOrPullRequest(number:) }` read.
- **Bare `#n`** names no repo. Search for it in the fleet's scope (`#703 (repo:… OR user:…)` matches the number) or, for the fleet's checked-out repos only, read `issueOrPullRequest(number: n)` once per repo with aliases in one query. Show every repo that has that number, parents first, and never guess.
- **Reuse:** the orders the client already holds carry `sub_issues` and `parent`, so the modal can show local matches at once before GitHub answers. The fleet's scope comes from `Router.Claims`, which `GET /api/sources/{key}/repos` already serves. Following still goes through `POST /api/campaigns`.

## What Admiral already has

### The GitHub client: the panels already run this search

`internal/github/client.go` calls GitHub only through `gh api graphql`, with the token from `gh auth token -h <host> [-u <account>]` (`Client.Token`). Each fleet has one built-in GitHub source with its own host and optional account (`internal/github/settings.go`: `Host`, `Account`), and the fleet's `env` is added to its `gh` calls (`docs/cli.md`). So a search runs as the fleet's own GitHub account, the same account ADR 0027 requires for every campaign read and write.

The panel search (`searchQueryWith`) is already:

```graphql
search(query: $q, type: ISSUE_ADVANCED, first: 50) { nodes {
  ... on Issue { number title url createdAt bodyText author { login } repository { nameWithOwner url }
                 labels(first: 100) { nodes { name } } subIssuesSummary { total } parent { … } } … } }
```

`Item.SubIssues` and `Item.Parent` come from it. Its limits matter for a type-ahead:

- `search()` appends `sort:updated-desc` unless the query names a sort (`hasSort`), so it never gives GitHub's best-match order.
- `MaxItems` is 50 and the fields are heavy (body text, 100 labels, PR rollups), with a 60-second `SearchTimeout` sized for background panel refreshes.
- `runQuery(…, optional, …)` tolerates partial errors only on `parent`, `stack` and `stackEntry` paths (`onlyOptionalErrors`), so an aliased per-repo lookup that misses in some repos would need its paths added.

A modal search wants its own light query: number, title, url, state, repository, `subIssuesSummary { total }`, `parent { number title url }`, perhaps labels, `first: 20`, the short 8-second `ghTimeout`, and no forced sort.

### How panels scope to a fleet today, and the scope a search would use

Panel queries are not scoped to repos. `githubConn.refresh` (`internal/sources/github.go`) runs each view's query as written, then drops every item whose repo doesn't route to the panel's fleet (`router.Route(host, it.Repo) != g.fleet`).

Routing is in `internal/github/route.go`. A fleet owns a repo it has checked out under its search roots (a **project**), then repos of the **owners** it claims (`owners` in config), and the first fleet (the **default fleet**) takes everything else. `Router.Claims(fleet, host)` lists the projects and owners a fleet wins, and `GET /api/sources/{key}/repos` serves exactly that (`serveRepos` in `internal/sources/service.go`).

Searching then filtering the way panels do fails for free text: a 20-hit search over all of GitHub's readable issues would rarely land in the fleet. The modal search has to put the scope into the query:

```text
is:issue (repo:o/a OR repo:o/b OR user:o2) <text>
```

What I checked against github.com with `gh` 2.104:

- Space-separated `repo:a repo:b` is an AND in advanced search and returned 0 hits; `repo:a,b` also returned 0. The scope needs `OR` inside parentheses.
- 81 `repo:` terms joined with `OR` (1,600 characters) were accepted, and repos that don't exist were ignored. GitHub's docs cap `user`/`org` qualifiers at 16 and put no limit on `repo` qualifiers ([filtering and searching issues](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/filtering-and-searching-issues-and-pull-requests)), and allow parentheses five levels deep. The REST search docs' "no more than five AND, OR or NOT operators" ([REST search](https://docs.github.com/en/rest/search/search)) did not bite on the GraphQL `ISSUE_ADVANCED` search.
- The **default fleet** owns "everything unclaimed", which can't be written as a scope. Search it over its own projects and owners; an issue elsewhere is still reachable by pasting `owner/repo#n` or its URL, which routes as today.

### Following a reference

- `web/src/app/campaignsLink.ts` `followReference(reference)` posts `{ reference }` to `POST /api/campaigns` and opens the campaign in its own fleet.
- `web/src/app/campaign.ts` `pasteHint` only warns about two shapes the server refuses: a comment link (`#issuecomment`) and a trailing slash. `CampaignPage.tsx` `PasteField` shows it and calls `followReference` on Enter.
- The server parses with `campaigns.ParseIssueRef` (`internal/campaigns/campaigns.go`): an `http(s)://host/owner/repo/issues/n` URL or `owner/repo#n`, with a pull request URL refused by name. `#n` alone is refused. A host-less `owner/repo#n` means `github.com` (`hostOf` in `internal/campaigns/follow/follow.go`).
- `follow.Service.Follow` routes the repo to a fleet (`s.Route`), reads the tree with that fleet's client (`readTree`), and stores it. `Owner(reference)` answers which fleet a reference belongs to without reading GitHub. Follow does not refuse a leaf issue.

So `owner/repo#n` and URLs need nothing new on the server. The modal can classify input in the client (URL, `owner/repo#n`, `#n` or bare digits, else text) and send the first two straight to a lookup or to Follow.

### What the client already holds

The orders of every panel reach the client with `sub_issues` and `parent` (`Order` in `web/src/app/api.ts`), and `OrderPane.tsx` already offers **Follow campaign** when `sub_issues > 0`. Filtering these in memory gives instant results, parents first, for the issues the engineer already sees (assigned to them, their views), before the network answers. The followed campaigns (`campaignList`) can be shown as "already followed" in results, since Follow returns the existing campaign rather than a new one.

The campaign tree read (`internal/github/tree.go`) is a per-issue `repository.issueOrPullRequest(number)` query with `subIssues { totalCount }`. It is the model for a single-issue lookup, but it reads far more (body, blockers, every child) than a search result needs.

## The calls

### Text search

```graphql
query($q: String!) {
  search(query: $q, type: ISSUE_ADVANCED, first: 20) {
    nodes { ... on Issue { number title url state repository { nameWithOwner }
                           subIssuesSummary { total completed } parent { number title url } } }
  }
}
```

with `$q = is:issue (repo:… OR user:…) <text>`. Add `is:open` unless the engineer asks for closed ones; ADR 0027 follows closed parents with open children, so closed parents may still be wanted.

Measured on this repo: cost 1 point (from `rateLimit { cost }`), 0.77 to 0.91 s wall time including starting `gh`.

### Parents first

The December 2024 changelog says "You can use the `has:` and `no:` filters to search for sub-issues and issue types", with `has:sub-issue` and `no:parent-issue` as examples ([changelog](https://github.blog/changelog/2024-12-12-github-issues-projects-close-issue-as-a-duplicate-rest-api-for-sub-issues-and-more/)). Checked on this repo with `ISSUE_ADVANCED`:

| Qualifier | Result for `is:issue campaign` (44 hits) |
| --- | --- |
| `has:sub-issue` | 5 hits, all parents |
| `-has:sub-issue` or `no:sub-issue` | 39 hits, all leaves |
| `has:sub-issues` (plural) | **silently ignored**: 44 hits, leaves included |
| `-has:sub-issues` (plural) | GitHub error ("Something went wrong") |
| `has:parent-issue`, `no:parent-issue` | work |
| `parent-issue:owner/repo#n` | works (lists that parent's sub-issues); the 2026-11-09 campaigns note found it returned nothing, so it has changed since |
| `(has:sub-issue OR no:parent-issue)` | 0 hits: an OR of these filters doesn't work |

Two ways to rank:

1. **Two queries in parallel**: `… has:sub-issue <text>` (parents) and `… <text>` (everything), merged with parents on top and duplicates dropped. Parents beyond the top 20 of the plain search still surface. 2 points.
2. **One query, sorted locally**: stable-partition the hits on `subIssuesSummary.total > 0`. 1 point, but a parent ranked 25th by GitHub never shows.

Option 1 is the better fit for a modal whose point is finding a campaign. The REST equivalent returns `sub_issues_summary { total, completed, percent_completed }` on each item ([sub-issues REST](https://docs.github.com/en/rest/issues/sub-issues)).

Caveat: `subIssuesSummary` counts real sub-issues only. A tree that links children by a `Part of #n` line in the body (past GitHub's 100-sub-issue limit, which ADR 0027's reader also handles) looks like a leaf to search unless it has some real sub-issues as well.

### `owner/repo#n` and URLs

One read, the shape `tree.go` uses, trimmed:

```graphql
{ repository(owner: "o", name: "r") { issueOrPullRequest(number: 703) {
    __typename ... on Issue { title url state subIssuesSummary { total } parent { number title url } } } } }
```

Cost 1, about 0.6 s. A pull request answers `__typename: PullRequest`, which the modal can refuse the way `ParseIssueRef` already refuses a pull URL. Route it with `Owner(reference)` to show which fleet it would join, or a refusal when it routes to another fleet than the modal's.

### Bare `#n`

`#n` names no repo, and ADR 0027 routes by repo, so there is no single right answer. Two ways, both cheap:

- **Search the number in the fleet's scope.** `is:issue (repo:… OR user:…) 703` and `… #703` both returned exactly that issue on this repo (a bare number token matches the issue number; `user:o 703` found it across the owner's repos). This covers owner-claimed repos the fleet hasn't checked out. One point.
- **Look it up in each project.** Aliases read the number in every checked-out repo in one query (`a: repository(…) { issueOrPullRequest(number: 703) … } b: …`). Cost 1 for two repos, 0.57 s. A repo without that number answers `null` with a `NOT_FOUND` error on its alias path, and `gh` exits non-zero. Admiral's `runQuery` would treat that as a failure today, because `optionalPath` doesn't cover it.

Either way, show every match with its repo, parents first, and let the engineer pick. If the fleet has a campaign **work repo** or one project, that one can lead. A plain number may also be text (a version, a port), so digits-only input should run both the number lookup and the text search.

### Semantic and hybrid search

GitHub's GraphQL `SearchType` now also has `ISSUE_SEMANTIC` ("using semantic search") and `ISSUE_HYBRID` ("hybrid (lexical + semantic) search") (schema introspection). They went GA on 2026-04-02 ([changelog](https://github.blog/changelog/2026-04-02-improved-search-for-github-issues-is-now-generally-available/)): REST `/search/issues?search_type=semantic|hybrid`, GraphQL `ISSUE_SEMANTIC`/`ISSUE_HYBRID`, scoped with `org:`, `user:` and `repo:`, falling back to lexical with a reason in the response, and "rate limited to 10 requests per minute". `gh search issues --search-type` exposes it too.

On this repo, "how do we pick a skill when launching" found nothing lexically and four relevant issues with hybrid or semantic, at the same cost (1 point) and latency (~0.9 s). Hybrid honored `has:sub-issue` and `is:open`. Hybrid's `issueCount` answered 0 while returning nodes, so the count can't be trusted there.

The 10-a-minute limit rules it out per keystroke. It fits a deliberate action: a "search by meaning" on Enter, or a fallback when the lexical search finds nothing.

## Cost, latency and rate limits

| Call | Limit it counts against | Cost | Measured latency |
| --- | --- | --- | --- |
| GraphQL `search` `ISSUE_ADVANCED` | GraphQL: 5,000 points an hour per user; secondary 2,000 points a minute, 100 concurrent requests, 90 s CPU per minute ([GraphQL limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api)) | 1 point for `first: 20` | 0.77 to 0.91 s |
| GraphQL `ISSUE_SEMANTIC` / `ISSUE_HYBRID` | also 10 requests a minute (changelog above) | 1 point | ~0.9 s |
| GraphQL `repository.issueOrPullRequest` (aliased) | GraphQL points | 1 point | ~0.6 s |
| REST `GET /search/issues` (`gh search issues` uses it) | search: 30 requests a minute; returns at most 1,000 results; queries over 256 characters (not counting qualifiers) or with more than five AND/OR/NOT fail ([REST search](https://docs.github.com/en/rest/search/search)) | 1 request | not measured |

- A GraphQL call costs the requests its connections could need, divided by 100 and rounded, with a minimum of 1 (GraphQL limits page). A 20-node search with one `parent` and a scalar summary per node rounds to 1.
- The GraphQL docs say nothing specific about search. I checked: after several GraphQL searches, `gh api rate_limit` still showed `search.used: 0`, while one REST `/search/issues` call moved it to 1. GraphQL search draws on the point budget, not the 30-a-minute bucket.
- `gh search issues` uses REST, and its JSON fields don't include sub-issue counts, so it is the wrong tool here.
- The engineer's panels already spend points every refresh (5 minutes by default, one search per view, plus event polls). A type-ahead debounced at ~300 ms with 2-character minimum, two queries per settled input, costs a few dozen points per modal session, small against 5,000 an hour. Cancel the in-flight `gh` when the input changes (the client takes a `ctx`).
- About 0.6 s of each call is `gh` start-up plus the round trip. Calling GitHub's GraphQL endpoint directly with the token would save the process start, but Admiral deliberately goes through `gh` (`client.go`), so this note assumes it stays.

## A shape this suggests

Not decided, here for the spec ticket to take or leave:

- A server route on the fleet's GitHub source, such as `GET /api/sources/{key}/issues?q=` or a campaigns route like `GET /api/campaigns/search?fleet=&q=`, that classifies the input, builds the scope from `Router.Claims`, runs the parents and plain queries in parallel through the fleet's client, and returns hits parents-first with `sub_issues`, `parent`, repo and whether the campaign is already followed.
- The client shows matching held orders and followed campaigns at once, then merges the server's answer.
- `owner/repo#n` and URLs skip search: one lookup, then Follow.
- Bare `#n` returns every fleet repo that has that number.
- Semantic or hybrid search, if wanted, on an explicit action only.

## Sources

- Code (on `main` at 1dd35c8b): `internal/github/client.go`, `route.go`, `settings.go`, `tree.go`; `internal/sources/github.go`, `service.go`, `connection.go`; `internal/campaigns/campaigns.go`, `follow/campaign.go`, `follow/follow.go`, `follow/routes.go`; `web/src/app/campaignsLink.ts`, `campaign.ts`, `CampaignPage.tsx`, `OrderPane.tsx`, `api.ts`; [ADR 0027](../adr/0027-campaigns-are-read-from-githubs-issue-tree.md); [campaigns research note](campaigns-issue-trees.md).
- GitHub docs: [REST search](https://docs.github.com/en/rest/search/search), [GraphQL rate and query limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api), [filtering and searching issues](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/filtering-and-searching-issues-and-pull-requests), [REST sub-issues](https://docs.github.com/en/rest/issues/sub-issues), [adding sub-issues](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues) (100 sub-issues per parent, eight levels).
- GitHub changelog: [has:/no: sub-issue filters, 2024-12-12](https://github.blog/changelog/2024-12-12-github-issues-projects-close-issue-as-a-duplicate-rest-api-for-sub-issues-and-more/), [improved issue search GA, 2026-04-02](https://github.blog/changelog/2026-04-02-improved-search-for-github-issues-is-now-generally-available/).
- GraphQL schema introspection of `SearchType` and `Issue`, and live queries with `gh` 2.104 against this public repo on 2026-11-17 for every measured cost, latency and qualifier result above.
