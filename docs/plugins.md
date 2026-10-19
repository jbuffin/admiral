# Writing an order source plugin

An order source is a program that gives Admiral a list of orders to launch ships from. You can write one in any language. Admiral starts it as a subprocess and talks to it in JSON-RPC 2.0, one message per line, on its stdin and stdout.

The freeform plugin in [`plugins/freeform/`](../plugins/freeform/) is the reference. It is a single Python file with no dependencies, and every example below is something it does. Read it next to this page. (ADR 0006 explains why plugins are subprocesses. After the MVP, a GitHub plugin becomes the worked example for sources with sign-in.)

## What you ship

A folder in `$ADMIRAL_HOME/plugins/<name>/` holding a `plugin.json`:

```json
{
  "name": "freeform",
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

An engineer installs a plugin by copying its folder into `plugins/`. Admiral writes only the freeform plugin itself, on a fresh install. It also keeps a `.admiral-stamp` file there, the SHA-256 of the folder's files, so it can replace the folder with a newer copy when you have not edited it. If you edit the freeform plugin, Admiral leaves your copy alone.

## Sources and settings

An engineer adds a *source* of your plugin: an entry in the `sources` object of `config.json`, keyed by a source key.

```json
"sources": {
  "orders": { "plugin": "freeform", "name": "Orders" },
  "work-board": { "plugin": "board", "name": "Work", "settings": { "project": "ADM" } }
}
```

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

`settings` is the object from `config.json` (`{}` when there is none). Answer with `protocolVersion` 1. Any other number puts the source in `error`, and Admiral does not start it again until the engineer saves its settings or presses Restart. `capabilities` lists what the source can do, from `add`, `edit`, `remove` and `refresh`; leave out what you don't support and Admiral refuses those calls itself with a 409. `settingsSchema` is a JSON Schema for `settings`, which Admiral renders as a form.

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
| `repo` | Optional git remote of the repository the order belongs to (`https://github.com/acme/widgets` or `git@github.com:acme/widgets.git`). Admiral uses it to preselect a checkout. |

Admiral hides an order while a ship launched from it exists, whatever that ship's status, and shows it again once the ship is gone, if you still list it. A plugin that wants a launched order to disappear for good deletes it when it hears `orders.launched`, as freeform does.

If you can't list a panel, say so with an `error` instead of a list. Its message (cut to one line of at most 300 characters; "This panel's orders couldn't be listed." if it is empty) is shown on that panel. The panel keeps the orders it had, the other panels and the source's state don't change, and the message stays until your next successful `orders.list` for that panel. A source in `error` shows its own error instead. A timeout or an invalid list isn't an error answer: Admiral logs it and shows nothing.

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

### Plugin → Admiral notifications

| Notification | Effect |
| --- | --- |
| `orders.changed` | Admiral asks `orders.list` again, for the panel in `params` or for every panel when there are none. Send it when your data changes, including after you handle `orders.add`. |
| `panels.changed` | Admiral asks `panels.list` again, then each panel's orders. |
| `auth.required` | The source shows as `auth_required` with the message, a link and a code to enter there. `message` is required; `url` and `code` are optional. Orders you listed stay. |
| `auth.ok` | Clears `auth.required`; the source is `ok` again. |

One full line of each:

```json
{"jsonrpc":"2.0","method":"orders.changed","params":{"panel":"orders"}}
{"jsonrpc":"2.0","method":"panels.changed"}
{"jsonrpc":"2.0","method":"auth.required","params":{"message":"Sign in to Acme","url":"https://acme.example/device","code":"ABCD-1234"}}
{"jsonrpc":"2.0","method":"auth.ok"}
```

**Signing in.** If your source needs the engineer to sign in, answer `initialize` and `panels.list` at once (an empty panel list is fine), then send `auth.required`. Never hold a request open while you wait for the engineer: it would hit the 10-second limit, count as a crash and put the source into a restart loop. Send `auth.ok` when sign-in is done, then `panels.changed` or `orders.changed` with the real data.

## Limits

Admiral treats everything a plugin sends as untrusted.

- A request that gets no answer within **10 seconds** fails. A hung `initialize` or `panels.list` also counts as a crash: Admiral kills the process and restarts it. Any other hung request fails only that call.
- A line over **16 MiB** is dropped.
- An order list over **5,000 orders**, or an order whose `text` is over **12,000 bytes**, is refused.
- At most **100 panels**. A panel id or an order id must be non-empty and unique within its list.
- `plugin.json` is at most 64 KiB. An `auth.required` message is cut to 2,000 characters and its code to 200.
- A control character (below 0x20, or 0x7f) in an order id or a panel id is refused. `text` may hold `\n`, `\t` and `\r` but no other control character. Titles are cut to one line.
- A `settingsSchema` that is not a JSON object, or is over 256 KiB, is replaced with `{}`.
- Error messages you answer with, and the text of a crash, are cut to one line of at most 300 characters.
- A `url` (in an order or in `auth.required`) must be `http:` or `https:`; Admiral drops any other. A `repo` that is not a git remote is dropped.

A **protocol error** is a line that is not JSON, a reply to a request that is not waiting, a list that breaks a limit, or a notification Admiral does not know. Admiral logs it, drops that message and keeps the process running. A list that breaks a limit is dropped whole, and the panel keeps the orders it had. Only a failed or hung `initialize` or `panels.list` counts as a crash.

## Crashes and what the engineer sees

When your process exits or is killed, the source shows `state: "error"` with a message. Its panels keep their last orders, but the source is **read-only**: Admiral refuses adding, editing, removing and refreshing with a 409. The engineer can still launch a ship from the orders, change the source's settings and press **Restart**. Admiral also restarts the process itself, after 1 second, then 2, 4 and so on up to 1 minute. The wait starts over at 1 second if the process had run for 60 seconds before it died. The source is writable again once the new process has answered `initialize` and `panels.list`.

If the process exits before it answers `panels.list`, the source has no panels to show, but its error and the Restart button still appear.

## Try it

Run the reference plugin by hand and type a request:

```
$ python3 plugins/freeform/plugin.py
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"settings":{},"dataDir":"/tmp/freeform-data"}}
{"jsonrpc":"2.0","id":2,"method":"orders.add","params":{"panel":"orders","text":"Fix the flaky test"}}
{"jsonrpc":"2.0","id":3,"method":"orders.list","params":{"panel":"orders"}}
```

Copy the folder to `$ADMIRAL_HOME/plugins/<name>/`, rename it in `plugin.json`, and add a source for it to `config.json` or from Admiral's Add source dialog.
