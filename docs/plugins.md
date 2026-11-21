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

A plugin that lists `page` in its `initialize` capabilities is a **module**: it declares a page of groups and rows with a detail view for each row. It stays on protocol version 1, and a plugin without `page` is not asked for any of this. Admiral draws everything from the answers below; a plugin never sends HTML or a color. This section is the whole contract: the calls, one table per object, how a row is drawn, a worked example and the limits. [Writing a module](#writing-a-module) is the checklist to build one.

### The calls

| Call | Direction | When | Answer |
| --- | --- | --- | --- |
| `page.get` | Admiral → plugin | Once the plugin is up, and again after each `page.changed` and `page.refresh`. | The page. |
| `row.get` | Admiral → plugin | The engineer opens a row of the last good page. | The row's detail. |
| `page.refresh` | Admiral → plugin | The engineer asks you to look again. | `{}` at once; send `page.changed` when you have new data. |
| `action.form` | Admiral → plugin | The engineer opens a module action whose `confirm` is `form`. | The form to fill in. |
| `action.run` | Admiral → plugin | The engineer confirms a module action. | `{message, url}`. |
| `page.changed` | plugin → Admiral | Your data changed. | None. |

**`page.get`** has no params. Answer the groups in order and the waiting count:

```json
{"jsonrpc":"2.0","id":5,"method":"page.get","params":{}}
{"jsonrpc":"2.0","id":5,"result":{"groups":[{"id":"review","title":"Needs review","rows":[{"id":"T-1","title":"Fix login","subtitle":"Acme","chips":[{"label":"P1","tone":"warn"}],"reason":"Review is open","action":{"id":"pass","label":"Pass review","confirm":"form","summary":"Pass the review"}}]}],"waiting":1}}
```

**`row.get`** carries the row `id` and, optionally, `include`. The `id` and the `include` values come from a client through Admiral, which checks only their length and that the id is on the page, so treat both as untrusted (never build a file path from them without checking). `include` lists parts you leave out by default, such as closed or queued steps; it is left out of the params when the engineer asked for none. The new shell asks for none today, so a part you hide by default stays hidden; `include` is for a client that asks (the route passes it on, see [the API](api.md)).

```json
{"jsonrpc":"2.0","id":6,"method":"row.get","params":{"id":"T-1","include":["closed"]}}
{"jsonrpc":"2.0","id":6,"result":{"sections":[{"kind":"links","title":"Links","links":[{"label":"Ticket","url":"https://example.com/t/1"}]},{"kind":"steps","title":"Steps","steps":[{"id":"s1","title":"Code review","chips":[{"label":"open","tone":"warn"}],"actions":[{"id":"pass","label":"Pass","confirm":"form"}]}]},{"kind":"fields","title":"Details","fields":[{"label":"Owner","value":"amy"}]}]}}
```

**`action.form`** and **`action.run`** are described under [Module actions](#module-actions-actionform-and-actionrun). **`page.changed`** and **`page.refresh`** are in [Writing a module](#writing-a-module).

### The objects

Each table says whether a field is required, what Admiral does with it and what happens when it is bad. "Dropped" means that part goes away and the rest of the answer stays; "refuses" means the whole answer is refused and the last good page stays served. Text fields (titles, subtitles, reasons and the like) are cut to one line of at most 200 characters, without invisible or bidirectional-control characters.

**The page** is the result of `page.get`.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `groups` | list of groups | yes | The groups in order. | Each group is a heading with its rows. | Not a list, over 100 groups: refuses. |
| `waiting` | number | no | Rows waiting on the engineer. | The module's badge in the navigation. | Negative or not a number: reads 0. |
| `notices` | list of notices | no | Lines about the module itself. | Above the groups. | Not a list, over 20 notices: refuses. |
| `loading` | boolean | no | `true` while your first upstream read runs. | While the source has no good page it stays `starting` and the notices show above a skeleton; `groups` and `waiting` are ignored. After a page was shown it changes nothing and the last good page stays. | Not a boolean: refuses. |

A group:

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `id` | string | yes | The group's id, unique among the page's groups. | Not shown. | Missing, not a string, holding a control character, over 200 characters or used twice: refuses. |
| `title` | string | no | The group's name. | The heading, with its row count. | Cut to one line. |
| `rows` | list of rows | yes | The rows in order. | One line each. | Not a list, over 5,000 rows across the page: refuses. |

**A row** is one item of a group.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `id` | string | yes | The row's id, unique across the whole page. | Names the row in `row.get`, in the address and in module action calls. | As a group's `id`: refuses. |
| `title` | string | no | What the row is. | The row's first line. | Cut to one line. |
| `subtitle` | string | no | A second line, such as an owner. | Under the title. In the detail view it links to `url`. | Cut to one line. |
| `url` | string | no | The row's own page upstream. | The detail view links the `subtitle` (or "Open" when there is none) to it, so you need no separate link. | Not `http(s):`: dropped, the row stays. |
| `chips` | list of chips | no | Short statuses. | Pills on the row and in the detail view. | Over 10: refuses. |
| `reason` | string | no | Why the row is in its group. | After the subtitle; also on the next step's card. | Cut to one line. |
| `target` | target | no | What the row's module actions act on. | A line of its own; see [how a row is drawn](#how-a-row-is-drawn). | See the target table. |
| `action` | action | no | The row's one main action. | Leads the buttons it sits with, filled. | See the action table. |
| `actions` | list of actions | no | Further actions. | Buttons on the row. | Over 10 actions on the row (its `action` counts as one): refuses. |

**A target** says what a row's module actions act on, such as the one stage of its work that is waiting.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `label` | string | yes | The thing acted on. | Bold on the target line, after an arrow. | Missing: the whole target is dropped. |
| `chips` | list of chips | no | Its statuses. | Pills after the label. | Over 10: refuses. |
| `step` | string | no | The `id` of that step in the row's detail view. | Puts that step first in the detail view, marked "Next up". | Not a usable id: dropped by itself, the target stays. |

**A chip** is a short status pill.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `label` | string | yes | The chip's text. | Inside the pill. | Missing: the chip is dropped. |
| `tone` | string | no | `neutral`, `good`, `warn` or `muted`. | The pill's color. | Any other value reads `neutral`. |

**An action** is a button, or, for a module action with `menu`, an item in a ⋯ menu. It is the `action` of a row, one of its `actions`, an action of a step, or the `action` of a notice.

| Field | Type | Required | Applies to | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- | --- |
| `id` | string | yes | all | The action's id, unique within a row and its steps (see [Module actions](#module-actions-actionform-and-actionrun)). It is cleaned and capped like a title (one line, no invisible characters, at most 200 characters), never refused for its length or a control character; the cleaned id is what `action.run` carries. | Not shown. | Missing, not a string or empty after cleaning: the action is dropped. |
| `label` | string | yes | all | The button's text. | The button's text. | Missing: the action is dropped. |
| `confirm` | string | no | module actions | `click` runs at once, `yesno` asks first, `form` asks `action.form` and shows its fields. | Decides what a press does first. | Any other value reads `yesno`. A message action always asks. |
| `summary` | string | no | module actions | A line of what the action does, shown in its confirm. | In the confirm. | Cleaned of control characters. |
| `step` | string | no | module actions | The `id` of the step in the row's detail view that the action acts on; see [how a row is drawn](#how-a-row-is-drawn). | Places the button; see how a row is drawn. | Not a usable id: dropped by itself, the action stays. Dropped on a built-in action and on a notice's action. A step the detail lacks is not an error. |
| `menu` | boolean | no | module actions | `true` puts the module action in its group's ⋯ menu instead of a button; see [how a row is drawn](#how-a-row-is-drawn). | An item of the group's ⋯ menu, which is named "More actions". | Not a boolean: reads `false`. Cleared on a built-in action, on a notice's action and on a row's `action`. |
| `builtin` | string | no | built-in actions | `open`, `launch` or `message`: Admiral handles the action itself and never sends it to the plugin. | A link or a dialog, not a plugin call. | Any other value, or `open` or `launch` with no `url`: the action is dropped. |
| `url` | string | for `open`, `launch` | built-in actions | An `http(s):` link. A bare `url` with no `builtin` is `open`. | The link or issue the button opens. | Not `http(s):`, or holding whitespace of any script or an invisible character: the action is dropped. |
| `kind`, `ref`, `title`, `link` | strings | for `message` | message actions | See below. | In the message confirm. | Any missing, or `link` unacceptable: the action is dropped. |

An action with no `builtin` is a module action: Admiral runs it through `action.form` and `action.run`. The built-in actions are:

- **Open link** (`builtin: "open"` with an http(s) `url`; a bare `url` with no `builtin` is the same thing). It opens the link. A `url` that is not http(s), or that holds whitespace of any script or an invisible character, drops the action; so does an empty `builtin` with no `url`.
- **Launch** (`builtin: "launch"` with an http(s) `url`). It opens the launch dialog on a GitHub issue. Admiral refuses, with a reason the page shows, a `url` that is not an `https` GitHub issue on a host the routed fleet uses, and an issue that already has a ship (there is no "anyway"). The dialog shows the fleet the issue routes to. A module never launches on its own rows, so a `url` is always a GitHub issue.
- **Message AdmiralBot** (`builtin: "message"` with `kind`, `ref`, `title` and `link`). `kind` must be one you declared in `messageKinds`, `link` is an http(s) URL of at most 500 bytes with no whitespace or invisible character, and `ref` is cut to at most 40 characters with its spaces collapsed. The action is dropped when any field is missing, the link is not acceptable, or the kind is not one you declared. Admiral composes the text itself from the kind's `job`, the `ref`, the `title` and the `link`, and shows it all in a yes/no confirm whatever `confirm` says; no other text of yours reaches AdmiralBot. The `title` is cut to one line of at most 120 characters and sent fenced as quoted data, and invisible characters are stripped. The engineer's confirm posts exactly the text it showed to the chat of the module's fleet, marked `*From the <page>:*`.

A built-in action needs no live plugin. While the page is in `error`, Open link and Launch still work and module actions and messages are off.

**A notice** is a line about the module itself (an update is waiting, a setting to choose, an upstream read is failing).

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `id` | string | yes | Follows the row id rules, unique among the notices. | Not shown. | Missing, invalid or used twice: refuses. |
| `text` | string | yes | What to say. | One line of at most 300 characters. | Empty after cleaning: the notice is dropped. |
| `tone` | string | no | `info`, `warn` or `error`. | The line's color. | Any other value reads `info`. |
| `link` | `{label, url}` | no | A link. | Opens in a new tab. A notice's link carries no subtitle or chips. | No label or a `url` that is not `http(s):`: the link is dropped, the notice stays. |
| `action` | action | no | One module action, for a choice in front of the engineer (such as "Pick your project"). | A button. A notice has no `actions` list. | Built-in actions are dropped; one with no `id` or `label`, or a bad `url`, is dropped; the notice stays. `step` is dropped. It counts toward no action limit. |

The engineer can't dismiss a notice, and a notice never counts toward `waiting` or the navigation badge, whatever its tone. Notices belong to the last good page: they stay while the source is `auth_required` or `error`.

```json
{"jsonrpc":"2.0","id":5,"result":{"groups":[],"waiting":0,"notices":[{"id":"update","text":"An update is waiting","tone":"warn","link":{"label":"Release notes","url":"https://example.com/releases"}}]}}
```

**The detail** is the result of `row.get`.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `sections` | list of sections | yes | The sections in order. | Under the row's title and chips. | Over 1 MiB in all: refuses. A section of an unknown `kind` is dropped. |

**A section** has a `kind` and a `title`, and holds the list its kind names.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `kind` | string | yes | `links`, `steps` or `fields`. | Picks the layout. | Any other value: the section is dropped. |
| `title` | string | no | The section's heading. | A heading with the item count. | Cut to one line. |
| `links` | list of links | for `links` | The links. | A list of links. | A link that is bad drops itself. |
| `steps` | list of steps | for `steps` | The steps. | A card each. | A step that is bad drops itself. |
| `fields` | list of fields | for `fields` | The facts. | Pairs of name and value. | A field that is bad drops itself. |

**A step** is one stage of the row's work.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `id` | string | yes | The step's id, unique across the detail. | Not shown; named by `target.step` and `action.step`. | Not usable: the step is dropped. Used twice: refuses. |
| `title` | string | no | What the step is. | The card's heading. | Cut to one line. |
| `subtitle` | string | no | A line under the title, such as who holds the step. | Under the heading. | Cut to one line. |
| `chips` | list of chips | no | Its statuses. | Pills beside the heading. | Over 10: refuses. |
| `actions` | list of actions | no | The step's own module actions. | Buttons on the card, always. A `step` on them is ignored for placement. | Over 10: refuses. |

**A link** is one item of a `links` section.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `label` | string | yes | The link's text. | The link, opening in a new tab. | Missing: the link is dropped. |
| `url` | string | yes | Where it goes. | The link's target. | Not `http(s):`: the link is dropped. |
| `subtitle` | string | no | A second line, such as a pull request's title. | Muted, under the link. | Cut to one line. |
| `chips` | list of chips | no | Statuses, such as "Merged". | Pills before the link. | Over 10: refuses the detail view, even when the link's `url` is bad. |

**A field** is one fact in a `fields` section.

| Field | Type | Required | Meaning | Drawn as | When bad |
| --- | --- | --- | --- | --- | --- |
| `label` | string | yes | The fact's name. | Beside the value. | Missing: the field is dropped. |
| `value` | string | no | The fact. | Beside the name. | Holding a control character other than a line break or tab: the field is dropped. |

### How a row is drawn

A row with no `target` shows its `action` and `actions` as one set of buttons, whatever `step` they carry. A row with a `target` draws its module actions by what they act on:

- **On the row, beside the target:** the module actions whose `step` equals `target.step`. The row's `action` leads (filled) when it is one of them. A `target` with no `step` holds none.
- **On the row, after a divider:** the module actions with no `step`, which act on the whole row, and the row's `action` leads them when it is one. When the `target` has no `step`, every module action is here. These sit outside the target's group, so its accessible name ("Acts on" and its label) doesn't claim they act on the target. The built-in actions come last, apart.
- **Not on the row:** a module action whose `step` names another step. It shows only in the detail view, on that step's card.
- **In the detail view, on a step's card:** the row's module actions naming that step, then the step's own `actions`. The step `target.step` names comes first, marked "Next up", with the row's `reason`, and its lead is the row's `action` when that names it.
- **In the detail view, after the steps:** the module actions with no `step`, and the ones whose `step` names no step of the detail.

Each of these groups draws its module actions with `menu: true` as the items of one ⋯ menu after the group's buttons, in the order given. A group with a single such action still gets the menu. Choosing an item acts like pressing a button: a `yesno` action opens its confirm, a `form` action its form, and a `click` action runs at once. The ⋯ button is off while the page is in `error` and while a run is out on the row, and shows a spinner while one of its items runs. The row's `action` is always a button, whatever its `menu` says.

An `id` shows once on the whole row, in the order the row's `action`, its `actions`, then each step's actions in step order. An action whose `id` already showed earlier in that order shows nowhere else.

### A worked example

A made-up tool, a CI pipeline. A row is one pipeline run, its steps are the run's stages, and the target is the failing stage. "Rerun stage" acts on that stage, "Skip smoke test" on another stage and "Cancel run" on the whole run. The commits that triggered it are links with chips and a subtitle.

`page.get` answers one group with one row:

```json
{"jsonrpc":"2.0","id":5,"result":{"groups":[{"id":"runs","title":"Runs","rows":[{"id":"run-41","title":"Release build","subtitle":"Run 41 on main","url":"https://ci.example/runs/41","chips":[{"label":"Failed","tone":"warn"}],"reason":"The deploy stage failed","target":{"label":"Deploy stage","chips":[{"label":"Timed out","tone":"warn"}],"step":"deploy"},"action":{"id":"rerun","label":"Rerun stage","confirm":"click","step":"deploy"},"actions":[{"id":"cancel","label":"Cancel run","confirm":"yesno"},{"id":"skip","label":"Skip smoke test","confirm":"yesno","step":"smoke"}]}]}],"waiting":1}}
```

On the row, "Rerun stage" sits beside "Deploy stage" and leads. "Cancel run" follows a divider. "Skip smoke test" names another stage, so it isn't on the row. `row.get` answers the detail:

```json
{"jsonrpc":"2.0","id":6,"result":{"sections":[{"kind":"links","title":"Commits","links":[{"label":"a1b2c3d","url":"https://ci.example/commit/a1b2c3d","subtitle":"Raise the retry limit","chips":[{"label":"Merged","tone":"good"}]},{"label":"e4f5a6b","url":"https://ci.example/commit/e4f5a6b","subtitle":"Fix the cache key","chips":[{"label":"Open","tone":"warn"},{"label":"2 reviews","tone":"muted"}]}]},{"kind":"steps","title":"Stages","steps":[{"id":"build","title":"Build","subtitle":"Finished in 3 minutes","chips":[{"label":"Passed","tone":"good"}]},{"id":"smoke","title":"Smoke test","subtitle":"Waiting for the deploy","chips":[{"label":"Pending","tone":"muted"}]},{"id":"deploy","title":"Deploy","subtitle":"Timed out talking to the registry","chips":[{"label":"Failed","tone":"warn"}],"actions":[{"id":"logs","label":"Fetch logs","confirm":"click"}]}]},{"kind":"fields","title":"Details","fields":[{"label":"Branch","value":"main"}]}]}}
```

The detail view lists the commits with their chips and subtitles, then the stages with Deploy first ("Next up", "Rerun stage" leading, then "Fetch logs"), then Build, then Smoke test with "Skip smoke test". "Cancel run" sits after the stages.

### Module actions: `action.form` and `action.run`

A module action is the `action` of a row, one of its `actions`, an action of one of its steps, or the `action` of a notice (never a built-in). Its `id` is unique within a row and its steps: when several carry the same `id`, the first wins, in the order the row's `action`, its `actions`, then each step's actions in section and step order, and Admiral ignores the later ones. Admiral knows a step's module actions only from the last good `row.get` of that row, and forgets them when the row leaves the page. A notice's action is known from the notices Admiral serves, so a notice on a `loading: true` answer before the first good page can be acted on once the source is running. Admiral never asks you about an `id` it does not know, a built-in action (Open link, Launch or Message AdmiralBot), or (for `action.form`) one whose `confirm` is not `form`.

`action.form` is sent when the engineer opens a module action whose `confirm` is `form`, and answers the form to fill in. It must answer within the 10-second call limit.

```json
{"jsonrpc":"2.0","id":7,"method":"action.form","params":{"row":"T-1","action":"pass"}}
{"jsonrpc":"2.0","id":7,"result":{"title":"Pass review","fields":[{"id":"whose","label":"Whose work","type":"choice","options":[{"id":"a","label":"Amy"},{"id":"b","label":"Bo"}],"default":"b"},{"id":"files","label":"Files","type":"checklist","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}],"default":["y"]},{"id":"time","label":"Time","type":"duration","default":"01:30","min":"00:15"}]}}
```

A form is `{title, fields}`; `title` is optional. A form field is `{id, label, type, options, default, min}`, and `type` is `choice` (pick one of `options`), `checklist` (tick any of `options`) or `duration`. An option is `{id, label}`. A duration value is `"hh:mm"` (two digits, minutes 00 to 59). The engineer may type it as `1:30`, `0:1`, `90` (minutes), `1.5` (hours), `1h30` or `45m`; Admiral always sends you `"hh:mm"`, so a label needs no format hint. `default` is optional: a choice's is an option id, a checklist's is a list of option ids, a duration's is `"hh:mm"`. A checklist with no `default` starts all ticked. `min` is the least duration, `"hh:mm"`, and only a duration has it. There is no text field yet. Titles and labels are cut to one line of at most 200 characters. Admiral drops a `default` that does not fit its field (a choice default that is not one of the options, a checklist default naming an id that is not an option); the field stays, with no default. Admiral refuses the whole answer, and the engineer sees a one-line error, when:

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

### Page limits

Admiral keeps the last good page. A `page.get` or `row.get` that gets no answer within 10 seconds fails like any other call. A page in the `error` state turns the module's module actions off. Menu module actions count toward the 10 actions of a row or step like any other.

| Over this, or this | Result |
| --- | --- |
| More than **5,000 rows** or **100 groups**; `groups` or `rows` not a list | The answer is dropped whole, and the last good page stays served. |
| A group or row with no `id`, or one that is not a string, holds a control character or is over **200 characters**; a group id or a row id (across the whole page) used twice | Dropped whole. |
| More than **20 notices**; a notice with no valid `id`, or an `id` used twice | Dropped whole. |
| More than **10 chips** on a row, its target, a step or a link; more than **10 actions** on a row (its `action` counts as one) or a step | Dropped whole (the detail view, for a step or a link). |
| A detail view over **1 MiB**, or a step `id` used twice | The detail view is refused. |
| `notices` not a list, `loading` not a boolean, or an answer that is not an object | Dropped whole. |
| A notice whose text is empty after cleaning; a chip with no text; an action with no `id` or `label`; a link, field or step that is unusable; a section of an unknown `kind`; a link or action whose `url` is not `http:` or `https:`; a field whose `value` holds a control character other than a line break or tab | That part is dropped by itself, and the rest of the answer stays. |
| A negative or non-number `waiting`; an unknown chip `tone`; an unknown notice `tone` | Corrected: 0, `neutral`, `info`. |

#### A value of the wrong JSON type

What a wrong type does depends on where it sits. Read it from the table's three results:

| Where | Result |
| --- | --- |
| The answer, a group, a notice, a row or a section that is not an object; `groups`, `notices`, a group's `rows`, a row's `chips` or `actions`, `sections`, or a section's `links`, `steps` or `fields` that is not a list; `loading` that is not a boolean; the `id` of a group, a notice or a row that is not a string | Refuses the whole answer: `page.get` keeps the last good page and logs a protocol error, `row.get` fails and the row's pane shows the error. |
| A chip, link, field, action or step that is not an object; a step's `chips` or `actions` that is not a list; a step `id` that is not a string; a target that is not an object or whose `chips` is not a list; a `kind` that is not a string | Drops that object (the target, the section, the step and so on) and keeps the rest. |
| Any other value, such as a `title`, `subtitle`, `reason`, `label`, `text`, `tone`, `url`, `confirm` or `summary` that is not a string, or a `waiting` that is not a number | Drops the field: text reads as empty, a tone as `neutral` (`info` on a notice), `confirm` as `yesno`, `menu` as `false`, `waiting` as 0, and an object that needs the value (an action without its `id` or `label`, a link without its `url`) goes with it. |

When `row.get` errors or is refused (an RPC error, no answer in 10 seconds, an answer over the limit or with a bad shape), the engineer sees "Couldn't load this row." and the one-line reason in the pane, and the page and the other rows stay as they were. The reason is your error message, cleaned to one line, or Admiral's own for a refused answer.

A notice's text is cut to one line of at most 300 characters, without invisible or bidirectional-control characters. Titles, subtitles, reasons and labels are cut to one line of at most 200 characters, without invisible or bidirectional-control characters.

## Writing a module

A module is an ordinary plugin that also draws a page. This is the read side; the objects and their limits are in [A module's page](#a-modules-page), and the routes a client reads are in [the API](api.md#get-apisourceskeypage-and-get-apisourceskeyrowsid).

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

Everything you send is checked against the [page limits](#page-limits) and the last good page stays when an answer is refused. The example at the end declares a page.

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
- A module's page has its own limits: at most 10 chips on a row, a target, a step or a link, and the rest of the [page limits](#page-limits).
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

This sibling declares a page: one group with two rows and a detail view for each. Each row has a Finish `click` action: `action.run` marks the row done and answers a message, and the plugin then sends `page.changed` with that row. Its `page.refresh` marks the first row done and sends `page.changed` the same way. Save it as `$ADMIRAL_HOME/plugins/tasks/plugin.py` with this manifest:

```json
{ "name": "tasks", "description": "Two tasks on a page.", "command": "python3", "args": ["plugin.py"] }
```

```python
#!/usr/bin/env python3
import json
import sys

tasks = {"t1": {"title": "Fix login", "done": False}, "t2": {"title": "Update docs", "done": False}}


def page():
    rows = [{"id": i, "title": t["title"], "chips": [{"label": "done" if t["done"] else "open", "tone": "good" if t["done"] else "warn"}],
             "action": {"id": "finish", "label": "Finish", "confirm": "click"}}
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
    if method == "action.run":
        tasks[params["row"]]["done"] = True
        return {"message": "Finished " + tasks[params["row"]]["title"]}
    if method == "page.refresh":
        tasks["t1"]["done"] = True
    return {}


for line in sys.stdin:
    msg = json.loads(line)
    if "id" in msg:
        result = handle(msg["method"], msg.get("params") or {})
        print(json.dumps({"jsonrpc": "2.0", "id": msg["id"], "result": result}), flush=True)
        if msg["method"] in ("page.refresh", "action.run"):
            row = msg["params"]["row"] if msg["method"] == "action.run" else "t1"
            print(json.dumps({"jsonrpc": "2.0", "method": "page.changed", "params": {"rows": [row]}}), flush=True)
```
