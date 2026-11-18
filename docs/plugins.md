# Writing an order source plugin

An order source is a program that gives Admiral a list of orders to launch ships from. You can write one in any language. Admiral starts it as a subprocess and talks to it in JSON-RPC 2.0, one message per line, on its stdin and stdout.

The [example plugin](#try-it) at the end of this page is the reference. It is a single Python file with no dependencies, shown here and not shipped with Admiral. GitHub is built into Admiral, so there is no shipped plugin to read beside it. (ADR 0006 explains why plugins are subprocesses.)

## What you ship

A folder in `$ADMIRAL_HOME/plugins/<name>/` holding a `plugin.json`:

```json
{
  "name": "notes",
  "description": "A list of orders you type yourself.",
  "command": "python3",
  "args": ["plugin.py"],
  "icon": "icon.svg"
}
```

| Field | Rule |
| --- | --- |
| `name` | Must equal the folder name: letters, digits, `-` and `_`. A plugin whose manifest name differs is listed as invalid and cannot be added as a source. |
| `description` | One line, shown in the Add source dialog. |
| `command` | A command with no `/` is looked up on Admiral's `PATH` (`python3`, `node`). One with a `/` is a path relative to the plugin folder (`./run.sh`). There is no shell. |
| `args` | Passed to the command as they are. The working directory is the plugin folder, so `plugin.py` means `plugins/<name>/plugin.py`. |
| `icon` | Optional. A `.svg` or `.png` file name in the plugin folder. Admiral serves it at `/api/plugins/<name>/icon`. A name with a path in it is invalid. |

`GET /api/plugins` lists every folder: `[{name, description, icon_url, valid, error}]`. A broken manifest shows `valid: false` and an `error` saying why.

An engineer installs a plugin by copying its folder into `plugins/`. Admiral ships no plugin, and a source is added to a fleet from the sidebar; a source in `config.json` names its fleet with `fleet`. GitHub is built in and needs no folder. A start removes the retired `plugins/freeform` and `plugins/github` folders an older Admiral wrote, unless you edited them.

## Sources and settings

An engineer adds a *source* of your plugin: an entry in the `sources` object of `config.json`, keyed by a source key.

```json
"sources": {
  "work-board": { "plugin": "board", "name": "Work", "fleet": "work", "settings": { "project": "ADM" } }
}
```

`fleet` names a fleet defined under `fleets` in the same file (here, one called `work`).

One plugin can back several sources, each with its own settings, its own process and its own data directory, `$ADMIRAL_HOME/data/<source key>/`. The data directory is yours to keep state in. Admiral keeps it when the source is removed and never gives it to a new source.

## Lifecycle

1. Admiral starts your command, one process per source, with the plugin folder as its working directory. Your stderr goes to Admiral's log, so log there.
2. Admiral sends `initialize`. Nothing else is sent before you answer.
3. Admiral sends `panels.list`, then `orders.list` for each panel. The source shows as `starting` until this is done, then `ok`.
4. From then on Admiral never polls. It asks again only when you send a notification, when the engineer presses refresh, and after it has added, edited or removed an order for the engineer.
5. Admiral restarts the process when the engineer saves the source's settings or presses Restart, and stops it when the source is removed or Admiral quits. It closes your stdin first. Exit when it closes. Admiral kills the process group after a second.

## Messages

Requests are `{"jsonrpc":"2.0","id":1,"method":"…","params":{…}}`. Answer each with the same `id` and either `result` or `error` (`{"code":-32602,"message":"…"}`). Notifications have no `id` and take no answer. One message is one line of JSON; never put a raw newline inside it.

### Admiral → plugin

**`initialize`**

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"settings":{"project":"ADM"},"dataDir":"/Users/me/.admiral/data/work-board"}}
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"capabilities":["add","edit","remove"],"settingsSchema":{"type":"object","properties":{"project":{"type":"string"}}}}}
```

`settings` is the object from `config.json` (`{}` when there is none). Answer with `protocolVersion` 1. Any other number puts the source in `error`, and Admiral does not start it again until the engineer saves its settings or presses Restart. `capabilities` lists what the source can do, from `add`, `edit`, `remove`, `refresh` and `page`; leave out what you don't support and Admiral refuses those calls itself: with a 409 for `add`, `edit`, `remove` and `refresh`, and with a 404 for the page routes. `settingsSchema` is a JSON Schema for `settings`, which Admiral renders as a form. `page` declares a page (see [A module's page](#a-modules-page)); `messageKinds` is an optional list of the messages the module may later ask Admiral to send AdmiralBot.

**Settings and secrets.** Mark a secret with `"format": "password"` (or `"writeOnly": true`). Admiral never sends a stored value back to the page unless the schema declares it as a plain string, number or integer (or an `enum` or `const` of scalars) reached through `properties` and `items`, in nodes that use only common keywords (`type`, `properties`, `items`, `required`, `title`, `description`, `format`, `enum`, `const`, `default`, `examples`, the length and range limits, `$defs`, `definitions`, `additionalProperties`, `readOnly`, `deprecated`). Everything else comes back as `__admiral_unchanged__`, and saving that text keeps the stored value. Admiral restores a masked value inside a list by its position, so removing a row above a masked value restores the wrong stored value into it.

**`panels.list`**: a panel is one list of orders in the source.

```json
{"jsonrpc":"2.0","id":2,"method":"panels.list","params":{}}
{"jsonrpc":"2.0","id":2,"result":[{"id":"orders","title":"Orders"}]}
```

**`orders.list`**: the orders of one panel.

```json
{"jsonrpc":"2.0","id":3,"method":"orders.list","params":{"panel":"orders"}}
{"jsonrpc":"2.0","id":3,"result":[{"id":"a1f3","title":"Fix the flaky test","text":"Fix the flaky test\nsee the CI log","url":"https://example.com/issues/12","hot":true,"repo":"https://github.com/acme/widgets"}]}
```

| Field | Meaning |
| --- | --- |
| `id` | Required. Stable and unique within the list. Admiral uses it to tell which order a ship was launched from. |
| `title` | The line shown on the order. If it has several lines Admiral shows the first non-blank one. If you leave it out, the first line of `text` is used. |
| `text` | What a ship is launched with. If you leave it out, the title is used. |
| `url` | Optional link to the order. |
| `hot` | Optional. Marks an order that needs attention. |
| `labels` | Optional. An array of label-name strings (`["bug", "ready-for-agent"]`), in the order you want them shown. Admiral shows them as chips on the order and its filter matches them. A plugin that leaves it out gets no chips. |
| `sub_issues` | Optional. A positive integer: how many sub-issues the order's issue has. Admiral offers Follow on an order that has it. Only the built-in GitHub source sends it today; a plugin's value is read the same way, and `0`, a negative or a non-integer is dropped. |
| `pr` | Optional. A pull request's state: an object `{state, review, checks, mergeable, conflicts, ready}`, the fields of a ship's `prs[]` entry without `url`. Admiral shows it as chips on the order's row. Only the built-in GitHub source sends it today, for a pull request and not for an issue; a plugin's value that is not an object of that shape is dropped, and the order stays. |
| `repo` | Optional git remote of the repository the order belongs to (`https://github.com/acme/widgets` or `git@github.com:acme/widgets.git`). Admiral uses it to preselect a checkout. |

Admiral lists every order you return, and shows the ships launched from it on its row, whatever their status. A launch from an order that already has a ship is refused unless the engineer sends it anyway. A plugin that wants a launched order to disappear for good deletes it when it hears `orders.launched`, as the example plugin in [Try it](#try-it) does.

If you can't list a panel, say so with an `error` instead of a list. Its message (cut to one line of at most 300 characters; "This view's orders couldn't be listed." if it is empty) is shown on that panel. The panel keeps the orders it had, the other panels and the source's state don't change, and the message stays until your next successful `orders.list` for that panel. A source in `error` shows its own error instead. A timeout or an invalid list isn't an error answer: Admiral logs it and shows nothing.

**`orders.refresh`**: re-read your upstream. `panel` may be left out for all panels. When you have new data, send `orders.changed`. Only sent to sources with the `refresh` capability.

```json
{"jsonrpc":"2.0","id":4,"method":"orders.refresh","params":{"panel":"orders"}}
{"jsonrpc":"2.0","id":4,"result":{}}
```

**`orders.add`**: only with `add`.

```json
{"jsonrpc":"2.0","id":5,"method":"orders.add","params":{"panel":"orders","text":"Fix the flaky test\nsee the CI log"}}
{"jsonrpc":"2.0","id":5,"result":{"id":"a1f3"}}
```

The `id` must be the one `orders.list` later gives the new order: Admiral records the order's launch under it. An id is never reused for another order.

**`orders.edit`**: only with `edit`.

```json
{"jsonrpc":"2.0","id":6,"method":"orders.edit","params":{"panel":"orders","id":"a1f3","text":"Fix the flaky test again"}}
{"jsonrpc":"2.0","id":6,"result":{}}
```

**`orders.remove`**: only with `remove`.

```json
{"jsonrpc":"2.0","id":7,"method":"orders.remove","params":{"panel":"orders","id":"a1f3"}}
{"jsonrpc":"2.0","id":7,"result":{}}
```

For all three, Admiral has already refused text that is empty after trimming, over 12,000 bytes, or holding a control character other than `\n`, `\t` and `\r`. You can refuse more: answer with an `error` and its message (cut to one line of at most 300 characters) is shown to the engineer. The same goes for `orders.refresh` and, on the panel, for `orders.list`.

```json
{"jsonrpc":"2.0","id":8,"error":{"code":-32602,"message":"No such order"}}
```

**`orders.launched`**: a ship was launched from this order. The call carries only the order id, because a source's orders are all its own. If your process was down at the time, Admiral sends it after your next `initialize`. If Admiral itself restarts first, the call is lost. Admiral refuses a second launch of an order while its ship exists.

```json
{"jsonrpc":"2.0","id":9,"method":"orders.launched","params":{"id":"a1f3"}}
{"jsonrpc":"2.0","id":9,"result":{}}
```

**`events.catalog`**: the kinds of event you can raise, asked once after `panels.list`. Optional: a source that answers method-not-found (`-32601`), a result that is not a list or an error has no events, and that is not a crash.

```json
{"jsonrpc":"2.0","id":3,"method":"events.catalog","params":{}}
{"jsonrpc":"2.0","id":3,"result":[{"kind":"review_requested","title":"Review requested of you"},{"kind":"commented","title":"Commented in Mine","panel":"mine"}]}
```

Each kind has a `kind` and a `title`, and a `panel` (one of your panel ids) when it belongs to one panel. A routine can run on a kind you list; a kind you don't list is never delivered. `new_in_view` is a retired kind: Admiral drops it from your catalog and drops its events, and a saved routine that names it shows as broken and never runs.

### Plugin → Admiral notifications

| Notification | Effect |
| --- | --- |
| `orders.changed` | Admiral asks `orders.list` again, for the panel in `params` or for every panel when there are none. Send it when your data changes, including after you handle `orders.add`. |
| `panels.changed` | Admiral asks `panels.list` again, then each panel's orders. |
| `page.changed` | A module's page changed. Admiral asks `page.get` again. `params` may be `{"rows": ["<id>", …]}`, the rows whose detail changed; none means every row. See [Writing a module](#writing-a-module). |
| `auth.required` | The source shows as `auth_required` with the message, a link and a code to enter there. `message` is required; `url` and `code` are optional. Orders you listed stay. |
| `auth.ok` | Clears `auth.required`; the source is `ok` again. |
| `event` | Something happened upstream. `params` is `{"kind", "ref", "panel"?, "actor"?, "author"?}`: `kind` (and `panel`, when the catalog gave one) must match a catalog entry, `ref` is the `http(s):` URL of the item, `actor` is who did it and `author` who wrote the item, as logins. Admiral works out how far to trust it from those logins and the fleet's lists; a `tier`, `verified` or `viewer` you add is ignored, so an event from a plugin is never verified. |

```json
{"jsonrpc":"2.0","method":"event","params":{"kind":"review_requested","ref":"https://example.com/pr/7","actor":"bob","author":"amy"}}
```

One full line of each:

```json
{"jsonrpc":"2.0","method":"orders.changed","params":{"panel":"orders"}}
{"jsonrpc":"2.0","method":"panels.changed"}
{"jsonrpc":"2.0","method":"page.changed","params":{"rows":["T-1"]}}
{"jsonrpc":"2.0","method":"auth.required","params":{"message":"Sign in to Acme","url":"https://acme.example/device","code":"ABCD-1234"}}
{"jsonrpc":"2.0","method":"auth.ok"}
```

**Signing in.** If your source needs the engineer to sign in, answer `initialize` and `panels.list` at once (an empty panel list is fine), then send `auth.required`. Never hold a request open while you wait for the engineer: it would hit the 10-second limit, count as a crash and put the source into a restart loop. Send `auth.ok` when sign-in is done, then `panels.changed` or `orders.changed` with the real data.

## A module's page

A plugin that lists `page` in its `initialize` capabilities is a **module**: it declares a page of groups and rows with a detail view for each row. It stays on protocol version 1, and a plugin without `page` is not asked for any of this. Admiral draws everything from the answers below; a plugin never sends HTML or a color.

**`page.get`**: sent once the plugin is up, with no params. Answer the groups in order and the waiting count:

```json
{"jsonrpc":"2.0","id":5,"method":"page.get","params":{}}
{"jsonrpc":"2.0","id":5,"result":{"groups":[{"id":"review","title":"Needs review","rows":[{"id":"T-1","title":"Fix login","subtitle":"Acme","chips":[{"label":"P1","tone":"warn"}],"reason":"Review is open","action":{"id":"pass","label":"Pass review","confirm":"form","summary":"Pass the review"}}]}],"waiting":1}}
```

A row is `{id, title, subtitle, chips, reason, action, actions}`. `id` is required and the other fields are optional. `reason` says why the row is in its group, and `action` is the row's one main action. `actions` is an optional list of further actions. A chip is `{label, tone}`, and `tone` is one of `neutral`, `good`, `warn` or `muted`; any other value reads as `neutral`. An action is `{id, label, confirm, summary, url, builtin, kind, ref, title, link}`. `confirm` is `click`, `yesno` or `form` (anything else reads as `yesno`). An action with no `builtin` is a module action: Admiral runs it through `action.form` and `action.run` below. An action with a `builtin` is a built-in action, which Admiral handles itself and never sends to the plugin:

- **Open link** (`builtin: "open"` with an http(s) `url`; a bare `url` with no `builtin` is the same thing). It opens the link. A `url` that is not http(s), or that holds whitespace of any script or an invisible character, drops the action; so does an empty `builtin` with no `url`.
- **Launch** (`builtin: "launch"` with an http(s) `url`). It opens the launch dialog on a GitHub issue. Admiral refuses, with a reason the page shows, a `url` that is not an `https` GitHub issue on a host the routed fleet uses, and an issue that already has a ship (there is no "anyway"). The dialog shows the fleet the issue routes to. A module never launches on its own rows, so a `url` is always a GitHub issue.
- **Message AdmiralBot** (`builtin: "message"` with `kind`, `ref`, `title` and `link`). `kind` must be one you declared in `messageKinds`, `link` is an http(s) URL of at most 500 bytes with no whitespace or invisible character, and `ref` is cut to at most 40 characters with its spaces collapsed. The action is dropped when any field is missing, the link is not acceptable, or the kind is not one you declared. Admiral composes the text itself from the kind's `job`, the `ref`, the `title` and the `link`, and shows it all in a yes/no confirm whatever `confirm` says; no other text of yours reaches AdmiralBot. The `title` is cut to one line of at most 120 characters and sent fenced as quoted data, and invisible characters are stripped. The engineer's confirm posts exactly the text it showed to the chat of the module's fleet, marked `*From the <page>:*`.

A built-in action needs no live plugin. While the page is in `error`, Open link and Launch still work and module actions and messages are off. `waiting` is the count of rows waiting on the engineer.

**A module's notices.** The `page.get` result may also carry `notices`, a list of `{id, text, tone, link, action}` in the order to draw them: lines about the module itself (an update is waiting, a setting to choose, an upstream read is failing), drawn above the groups. `id` is required and follows the row id rules, unique among the page's notices. `tone` is `info`, `warn` or `error`; any other value reads as `info`. `link` is optional, `{label, url}` like a detail-view link, and opens in a new tab. `action` is optional, one module action in the shape of a row's `action` (the built-in actions, Open link, Launch and Message AdmiralBot, are row-only and are dropped on a notice; a notice links through its `link`), so a module can put a choice in front of the engineer (a setup choice such as "Pick your project"); a notice has no `actions` list. The engineer can't dismiss a notice, and a notice never counts toward `waiting` or the nav badge, whatever its tone. Notices belong to the last good page: they stay while the source is `auth_required` or `error`.

```json
{"jsonrpc":"2.0","id":5,"result":{"groups":[],"waiting":0,"notices":[{"id":"update","text":"An update is waiting","tone":"warn","link":{"label":"Release notes","url":"https://example.com/releases"}}]}}
```

**Loading.** `loading: true` says the plugin has not finished its first upstream read. While the source has no good page, the page stays `starting` and the shell draws its notices above the starting skeleton, so a plugin can say "Pick your project" before it has data; its `groups` and `waiting` are ignored. After a page has been shown, a `loading: true` answer changes nothing and the last good groups and notices stay. The next answer without `loading`, after a `page.changed`, draws normally. Admiral sets no time limit on loading.

**`row.get`**: sent when the engineer opens a row of the last good page. The `id` and the `include` values come from a client through Admiral, which checks only their length and that the id is on the page, so treat both as untrusted (never build a file path from them without checking). `include` is optional and lists parts you leave out by default, such as closed or queued steps; it is left out of the params when the engineer asked for none.

```json
{"jsonrpc":"2.0","id":6,"method":"row.get","params":{"id":"T-1","include":["closed"]}}
{"jsonrpc":"2.0","id":6,"result":{"sections":[{"kind":"links","title":"Links","links":[{"label":"Ticket","url":"https://example.com/t/1"}]},{"kind":"steps","title":"Steps","steps":[{"id":"s1","title":"Code review","chips":[{"label":"open","tone":"warn"}],"actions":[{"id":"pass","label":"Pass","confirm":"form"}]}]},{"kind":"fields","title":"Details","fields":[{"label":"Owner","value":"amy"}]}]}}
```

A detail view is a list of sections. A section has a `kind` and a `title`: `links` holds `links`, a list of `{label, url}`; `steps` holds `steps`, a list of `{id, title, chips, actions}`; `fields` holds `fields`, a list of `{label, value}`. A section of any other `kind` is dropped.

**Module actions: `action.form` and `action.run`.** A module action is the `action` of a row, one of its `actions`, an action of one of its steps, or the `action` of a notice (never a built-in). Its `id` is unique within a row and its steps: when several carry the same `id`, the first wins, in the order the row's `action`, its `actions`, then each step's actions in section and step order, and Admiral ignores the later ones. Admiral knows a step's module actions only from the last good `row.get` of that row, and forgets them when the row leaves the page. A notice's action is known from the notices Admiral serves, so a notice on a `loading: true` answer before the first good page can be acted on once the source is running. Admiral never asks you about an `id` it does not know, a built-in action (Open link, Launch or Message AdmiralBot), or (for `action.form`) one whose `confirm` is not `form`.

`action.form` is sent when the engineer opens a module action whose `confirm` is `form`, and answers the form to fill in. It must answer within the 10-second call limit.

```json
{"jsonrpc":"2.0","id":7,"method":"action.form","params":{"row":"T-1","action":"pass"}}
{"jsonrpc":"2.0","id":7,"result":{"title":"Pass review","fields":[{"id":"whose","label":"Whose work","type":"choice","options":[{"id":"a","label":"Amy"},{"id":"b","label":"Bo"}],"default":"b"},{"id":"files","label":"Files","type":"checklist","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}],"default":["y"]},{"id":"time","label":"Time","type":"duration","default":"01:30","min":"00:15"}]}}
```

A form is `{title, fields}`; `title` is optional. A field is `{id, label, type, options, default, min}`, and `type` is `choice` (pick one of `options`), `checklist` (tick any of `options`) or `duration`. An option is `{id, label}`. A duration value is `"hh:mm"` (two digits, minutes 00 to 59). `default` is optional: a choice's is an option id, a checklist's is a list of option ids, a duration's is `"hh:mm"`. A checklist with no `default` starts all ticked. `min` is the least duration, `"hh:mm"`, and only a duration has it. There is no text field yet. Titles and labels are cut to one line of at most 200 characters. Admiral drops a `default` that does not fit its field (a choice default that is not one of the options, a checklist default naming an id that is not an option); the field stays, with no default. Admiral refuses the whole answer, and the engineer sees a one-line error, when:

- it is over **64 KiB**, is not an object, or `fields` is not a list;
- there are more than **20 fields**, or a field has more than **100 options**;
- a `choice` or `checklist` field has no options (`options` missing or empty);
- a field has an unknown `type`, or a duration's `default` or `min` is not `"hh:mm"`;
- a field `id` or an option `id` is not a string, is empty, holds a control character or is over 200 characters, or a field `id` (across the form) or an option `id` (within its field) is used twice.

`action.run` is sent when the engineer confirms a module action, and carries what they filled in. It may take up to **60 seconds** to answer, an exception to the 10-second rule in Limits; the answer is `{message, url}`. Requests overlap: while a run is open Admiral still sends `page.get`, `row.get`, `panels.list` and other calls, each with its 10-second limit. Keep answering them while a run works, for example by handling each request on its own thread or task, as a sequential loop would let a long run time those calls out, turn the page to `error` and get a hung `panels.list` restarted mid-run.

The calls for a notice's action name the notice in place of the row: `action.form` gets `{"notice":"pick","action":"project"}` and `action.run` gets `{"notice":"pick","action":"project","values":{...}}`. Each call carries exactly one of `row` or `notice`, never both. A notice id and a row id are separate: a notice `pick` and a row `pick` can both have an action `project`, and the call says which it means.

```json
{"jsonrpc":"2.0","id":8,"method":"action.run","params":{"row":"T-1","action":"pass","values":{"whose":"b","files":["x","y"],"time":"01:30"}}}
{"jsonrpc":"2.0","id":8,"result":{"message":"Review passed","url":"https://example.com/t/1"}}
```

`values` is an object keyed by field id, whose values are strings or lists of strings; a module action with no form gets `{}`. Admiral checks only its shape and size (at most **50 fields**, a list of at most **100 items**, each string at most **1,000 characters**, a field id at most 200 characters, and a request body of at most 1 MiB) and passes it on. Validating the values against your form is your job, so treat them as untrusted. Answer a JSON-RPC error to refuse them: its message, cut to one line of at most 300 characters, is what the engineer sees, and nothing is assumed to have changed. `message` is required, and is cut the same way; `url` is optional and is left out unless it is an http(s) link of at most 2,000 bytes. An answer that is not an object, or has no `message`, is an error to the engineer. A module action runs one at a time per row or notice: Admiral refuses a second run on a row (or on a notice) until the first has answered or its 60 seconds have passed, even if the engineer's browser has gone away. Admiral sends no message when a run ends; send `page.changed` when the page moved.

**Message kinds.** `initialize` may also answer `messageKinds`, a list of `{kind, label, job}`, each one a string. Admiral keeps the ones that have all three, each cut to one line of at most 200 characters, and drops a bad declaration on its own.

**Page route.** The shell opens a module at `/app/<source key>`, and a row at `/app/<source key>/<row id>`. A source whose key equals a built-in surface id (`inbox`, `ships`, `orders`, `chat`, `campaigns`, `routines`, `crew`, `preferences`) gets no nav entry and its page can't be reached; the surface keeps its path.

**Page limits.** Admiral keeps the last good page. A `page.get` or `row.get` that gets no answer within 10 seconds fails like any other call. A page in the `error` state turns the module's module actions off.

An answer is dropped whole, and the last good page stays served, when:

- there are more than **5,000 rows** or **100 groups**, or `groups` or `rows` is not a list;
- a group or row has no `id`, or an `id` that is not a string, holds a control character or is over **200 characters**, or a group id or a row id (across the whole page) is used twice;
- there are more than **20 notices**, or a notice has no `id`, an `id` that is not a valid id (the rule above), or an `id` used twice among the notices;
- a row or step has more than **10 chips**, or more than **10 actions** (a row's main `action` counts as one);
- a detail view is over **1 MiB**, or uses a step `id` twice;
- `notices` is not a list, or `loading` is not a boolean;
- the answer is not an object.

A part that is bad is dropped by itself, and the rest of the answer stays:

- a notice whose text is empty after cleaning, which drops itself;
- a chip with no label, an action with no `id` or `label`, a link with no label, a field with no label, a step with no usable `id`, and a section of an unknown `kind`;
- a link or an action whose `url` is not `http:` or `https:`, and a notice's link with no label or such a url, which drops the link and keeps the notice;
- a field whose `value` holds a control character other than a line break or tab.

Three values are corrected: a negative or non-number `waiting` reads 0, an unknown chip `tone` reads `neutral`, and an unknown notice `tone` reads `info`. A notice's text is cut to one line of at most 300 characters, without invisible or bidirectional-control characters. A notice's `action` is read like a row's main action, except that a built-in action (Open link, Launch or Message AdmiralBot) is dropped: one with no `id` or `label`, or a `url` that is not `http:` or `https:`, is dropped and the notice stays. It counts toward no action limit. Titles, subtitles, reasons and labels are cut to one line of at most 200 characters, without invisible or bidirectional-control characters.

## Writing a module

A module is an ordinary plugin that also draws a page. This is the read side; the answers' shapes and limits are in [A module's page](#a-modules-page), and the routes a client reads are in [the API](api.md#get-apisourceskeypage-and-get-apisourceskeyrowsid).

1. **Declare `page`.** List `"page"` in the `capabilities` of your `initialize` answer. A plugin that does not is never asked for a page. You may also answer `messageKinds` there, a list of `{kind, label, job}` for the messages you want Admiral to be able to send; Message AdmiralBot actions use them (see [A module's page](#a-modules-page)).
2. **Answer `page.get`.** It comes during start-up, before the source is `ok`, and again after each `page.changed` and `page.refresh`. Answer the groups and the waiting count, and add `notices` to say something about the module itself. Send `loading: true` while your first upstream read is still running. A refused answer (over a limit, not valid) leaves the last good page served, and a call that fails with an error of yours shows the page as `error` with your message until the next good answer.
3. **Answer `row.get`.** It comes when the engineer opens a row of the last good page.
4. **Tell Admiral when your data changes.** Send `page.changed`. Name the rows whose detail changed to have open ones refetched, or send it without params to refetch every open row. Admiral asks `page.get` once for changes that arrive together, and drops a malformed list and an id that is not a valid id. A `rows` list of more than 5,000 items, or more than 5,000 distinct ids among the changes that coalesce into one `page.get`, counts as naming no rows, so every open row is refetched.

   ```json
   {"jsonrpc":"2.0","method":"page.changed","params":{"rows":["T-1","T-2"]}}
   {"jsonrpc":"2.0","method":"page.changed"}
   ```

5. **Handle `page.refresh`.** It comes with `{}` when the engineer asks you to look again. Start looking, answer `{}` at once (within 10 seconds), and send `page.changed` when you have new data, the way `orders.refresh` works. An answer that is an error shows to the engineer.
6. **Sign-in and crashes** work as for orders: send `auth.required` and the page shows `auth_required` with its last groups; a crash shows `error` and keeps them.

Everything you send is checked against the [limits](#a-modules-page) and the last good page stays when an answer is refused. The example below declares a page.

## Limits

Admiral treats everything a plugin sends as untrusted.

- A request that gets no answer within **10 seconds** fails. A hung `initialize` or `panels.list` also counts as a crash: Admiral kills the process and restarts it. Any other hung request fails only that call. `action.run` has **60 seconds**, and the other requests are still sent while it is open, so a plugin must answer them in the meantime.
- A line over **16 MiB** is dropped.
- An order list over **5,000 orders**, or an order whose `text` is over **12,000 bytes**, is refused.
- At most **100 panels**. A panel id or an order id must be non-empty and unique within its list.
- `plugin.json` is at most 64 KiB. An `auth.required` message is cut to 2,000 characters and its code to 200.
- A control character (below 0x20, or 0x7f) in an order id or a panel id is refused. `text` may hold `\n`, `\t` and `\r` but no other control character. Titles are cut to one line.
- A `settingsSchema` that is not a JSON object, or is over 256 KiB, is replaced with `{}`.
- Error messages you answer with, and the text of a crash, are cut to one line of at most 300 characters.
- An `event` with a `kind` the catalog lacks, a `ref` that is not an `http:` or `https:` URL, a `ref`, `kind` or `panel` over **512 bytes**, an `actor` or `author` over **100 bytes**, or a control, format or bidirectional-control character in any field is dropped. At most **60 events a minute** are delivered; the minute starts at the first event, the rest of it is dropped and Admiral logs how many once the minute ends. An event from a plugin names no repository, so a fleet whose repo allowlist is not empty skips it.
- A `url` (in an order or in `auth.required`) must be `http:` or `https:`; Admiral drops any other. A `repo` that is not a git remote is dropped.

A **protocol error** is a line that is not JSON, a reply to a request that is not waiting, a list that breaks a limit, or a notification Admiral does not know. Admiral logs it, drops that message and keeps the process running. A list that breaks a limit is dropped whole, and the panel keeps the orders it had. Only a failed or hung `initialize` or `panels.list` counts as a crash.

## Crashes and what the engineer sees

When your process exits or is killed, the source shows `state: "error"` with a message. Its panels keep their last orders, but the source is **read-only**: Admiral refuses adding, editing, removing and refreshing with a 409. The engineer can still launch a ship from the orders, change the source's settings and press **Restart**. Admiral also restarts the process itself, after 1 second, then 2, 4 and so on up to 1 minute. The wait starts over at 1 second if the process had run for 60 seconds before it died. The source is writable again once the new process has answered `initialize` and `panels.list`.

If the process exits before it answers `panels.list`, the source has no panels to show, but its error and the Restart button still appear.

## Try it

This example plugin keeps a list of orders in memory. It declares the protocol capabilities `add` and `remove`, so the view shows the add field and the remove button. Make the folder `$ADMIRAL_HOME/plugins/notes/` and save the manifest and the script in it:

```json
{ "name": "notes", "description": "A list of orders you type yourself.", "command": "python3", "args": ["plugin.py"] }
```

```python
#!/usr/bin/env python3
import json
import secrets
import sys

orders = []  # a real plugin keeps these in its data directory


def handle(method, params):
    if method == "initialize":
        return {"protocolVersion": 1, "capabilities": ["add", "remove"],
                "settingsSchema": {"type": "object", "properties": {}, "additionalProperties": False}}
    if method == "panels.list":
        return [{"id": "orders", "title": "Orders"}]
    if method == "orders.list":
        return [{"id": o["id"], "title": o["text"].strip().splitlines()[0], "text": o["text"]} for o in orders]
    if method == "orders.add":
        orders.append({"id": secrets.token_hex(6), "text": params["text"]})
        return {"id": orders[-1]["id"]}
    if method == "orders.remove" or method == "orders.launched":
        orders[:] = [o for o in orders if o["id"] != params["id"]]
        return {}
    return {}


for line in sys.stdin:
    msg = json.loads(line)
    if "id" in msg:
        result = handle(msg["method"], msg.get("params") or {})
        print(json.dumps({"jsonrpc": "2.0", "id": msg["id"], "result": result}), flush=True)
        if msg["method"] in ("orders.add", "orders.remove"):
            print(json.dumps({"jsonrpc": "2.0", "method": "orders.changed", "params": {"panel": "orders"}}), flush=True)
```

Run it by hand and type a request:

```console
$ python3 plugins/notes/plugin.py
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"settings":{},"dataDir":"/tmp/notes-data"}}
{"jsonrpc":"2.0","id":2,"method":"orders.add","params":{"panel":"orders","text":"Fix the flaky test"}}
{"jsonrpc":"2.0","id":3,"method":"orders.list","params":{"panel":"orders"}}
```

To use it in Admiral, add a source for it to `config.json` (with its `fleet`) or from Admiral's Add source dialog.

### A module

This sibling declares a page: one group with two rows and a detail view for each. Its `page.refresh` marks the first row done and sends `page.changed` with the row it changed. Save it as `$ADMIRAL_HOME/plugins/tasks/plugin.py` with this manifest:

```json
{ "name": "tasks", "description": "Two tasks on a page.", "command": "python3", "args": ["plugin.py"] }
```

```python
#!/usr/bin/env python3
import json
import sys

tasks = {"t1": {"title": "Fix login", "done": False}, "t2": {"title": "Update docs", "done": False}}


def page():
    rows = [{"id": i, "title": t["title"], "chips": [{"label": "done" if t["done"] else "open", "tone": "good" if t["done"] else "warn"}]}
            for i, t in tasks.items()]
    return {"groups": [{"id": "tasks", "title": "Tasks", "rows": rows}], "waiting": sum(not t["done"] for t in tasks.values()),
            "notices": [{"id": "demo", "text": "These tasks are examples", "tone": "info"}]}


def handle(method, params):
    if method == "initialize":
        return {"protocolVersion": 1, "capabilities": ["page"], "settingsSchema": {"type": "object", "properties": {}}}
    if method == "panels.list":
        return []
    if method == "page.get":
        return page()
    if method == "row.get":
        t = tasks[params["id"]]
        return {"sections": [{"kind": "fields", "title": "Details", "fields": [{"label": "Title", "value": t["title"]}]}]}
    if method == "page.refresh":
        tasks["t1"]["done"] = True
    return {}


for line in sys.stdin:
    msg = json.loads(line)
    if "id" in msg:
        result = handle(msg["method"], msg.get("params") or {})
        print(json.dumps({"jsonrpc": "2.0", "id": msg["id"], "result": result}), flush=True)
        if msg["method"] == "page.refresh":
            print(json.dumps({"jsonrpc": "2.0", "method": "page.changed", "params": {"rows": ["t1"]}}), flush=True)
```
