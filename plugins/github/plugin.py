#!/usr/bin/env python3
"""The GitHub order source: issues and pull requests found by saved searches.

It speaks Admiral's order source protocol (docs/plugins.md). Each panel is one
GitHub search; each issue or PR it finds is an order whose text is its URL.
All GitHub access goes through the gh CLI, and the plugin only reads. The token
comes from `gh auth token` and goes to each gh call as GH_TOKEN; it is never
logged or stored.
"""
import json
import os
import re
import subprocess
import sys
import threading

PROTOCOL_VERSION = 1
GH_TIMEOUT = 8  # seconds; Admiral gives a request 10
MAX_ORDERS = 50

DEFAULT_HOST = "github.com"
DEFAULT_PANELS = [
    {"title": "Assigned to me", "query": "is:open assignee:@me"},
    {"title": "Review requested", "query": "is:open is:pr review-requested:@me"},
]
DEFAULT_HOT_LABELS = ["priority: high", "P0", "P1", "urgent"]
DEFAULT_REFRESH_MINUTES = 5
MAX_REFRESH_MINUTES = 24 * 60

# What gh prints when the network, not GitHub, failed. It prints the first for
# a DNS error and the rest raw from Go's transport.
UNREACHABLE = ("error connecting to", "dial tcp", "i/o timeout", "TLS handshake", "x509:", "connection refused",
               "connection reset", "no route to host", "network is unreachable", "proxyconnect")

INVALID_PARAMS = -32602
METHOD_NOT_FOUND = -32601

FIELDS = "number title url repository { nameWithOwner url } labels(first: 100) { nodes { name } }"
SEARCH = ("query($q: String!) { search(query: $q, type: ISSUE, first: %d) { nodes { "
          "... on Issue { %s } ... on PullRequest { %s } } } }" % (MAX_ORDERS, FIELDS, FIELDS))

SETTINGS_SCHEMA = {
    "type": "object",
    "properties": {
        "host": {"type": "string", "description": "The GitHub host. Default: github.com"},
        "account": {"type": "string", "description": "The gh user to sign in as. Default: the host's active account"},
        "panels": {
            "type": "array",
            "description": "One panel per GitHub search. Default: Assigned to me, Review requested",
            "items": {
                "type": "object",
                "properties": {
                    "title": {"type": "string", "description": "The panel's title"},
                    "query": {"type": "string", "description": "A GitHub search, e.g. repo:acme/widgets is:open label:bug"},
                },
            },
        },
        "hotLabels": {
            "type": "array",
            "description": "Labels that mark an order hot, in any case. Default: priority: high, P0, P1, urgent",
            "items": {"type": "string"},
        },
        "refreshMinutes": {"type": "integer", "description": "Minutes between searches. Default: 5"},
    },
    "additionalProperties": False,
}


class RpcError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


class SignInNeeded(Exception):
    pass


class SearchFailed(Exception):
    pass


send_lock = threading.Lock()
state_lock = threading.Lock()
wake = threading.Event()

host = DEFAULT_HOST
account = None
panels = []  # [{"id", "title", "query"}]
hot_labels = set()
refresh_minutes = DEFAULT_REFRESH_MINUTES
cache = {}  # panel id -> {"orders": [...], "error": str or None}
auth_message = None  # the auth.required message in force, or None when signed in


def send(obj):
    with send_lock:
        sys.stdout.write(json.dumps(obj) + "\n")
        sys.stdout.flush()


def notify(method, params=None):
    msg = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        msg["params"] = params
    send(msg)


def text_setting(settings, key, default):
    value = settings.get(key)
    return value.strip() if isinstance(value, str) and value.strip() else default


def slug(title):
    return re.sub(r"[^a-z0-9]+", "-", title.lower()).strip("-") or "panel"


def read_panels(value):
    """The panels setting as panel dicts. An entry with no title or no query is
    skipped; with nothing left, the defaults stand."""
    entries = []
    for e in value if isinstance(value, list) else []:
        if not isinstance(e, dict):
            continue
        title, query = e.get("title"), e.get("query")
        if isinstance(title, str) and title.strip() and isinstance(query, str) and query.strip():
            entries.append({"title": title.strip(), "query": query.strip()})
    result, used = [], set()
    for e in entries or DEFAULT_PANELS:
        base, n = slug(e["title"]), 1
        pid = base
        while pid in used:
            n += 1
            pid = "%s-%d" % (base, n)
        used.add(pid)
        result.append({"id": pid, "title": e["title"], "query": e["query"]})
    return result


def read_hot_labels(value):
    labels = [s.strip().lower() for s in value if isinstance(s, str) and s.strip()] if isinstance(value, list) else []
    return set(labels or (s.lower() for s in DEFAULT_HOT_LABELS))


def read_minutes(value):
    if isinstance(value, int) and not isinstance(value, bool) and value >= 1:
        return min(value, MAX_REFRESH_MINUTES)
    return DEFAULT_REFRESH_MINUTES


def gh(args, token=None):
    """Run gh and return its stdout. Raises FileNotFoundError when gh is not
    installed, subprocess.TimeoutExpired on a hung call and
    subprocess.CalledProcessError (with stderr) when gh fails."""
    env = dict(os.environ, GH_NO_UPDATE_NOTIFIER="1", GH_PROMPT_DISABLED="1")
    env.pop("GH_TOKEN", None)
    if token:
        env["GH_TOKEN"] = token
    done = subprocess.run(["gh"] + args, env=env, capture_output=True, text=True,
                          timeout=GH_TIMEOUT, stdin=subprocess.DEVNULL)
    if done.returncode != 0:
        raise subprocess.CalledProcessError(done.returncode, args, done.stdout, done.stderr)
    return done.stdout


def get_token():
    args = ["auth", "token", "-h", host]
    if account:
        args += ["-u", account]
    try:
        token = gh(args).strip()
    except FileNotFoundError:
        raise SignInNeeded("Install the GitHub CLI (brew install gh), then run gh auth login")
    except subprocess.TimeoutExpired:  # a locked keychain, say: not a sign-out
        raise SearchFailed("Couldn't reach %s" % host)
    except subprocess.CalledProcessError:
        token = ""
    if not token:
        raise SignInNeeded("Run gh auth login -h %s" % host)
    return token


def search(query, token):
    """The orders a search finds. Raises SearchFailed with the message to show."""
    if not re.search(r"(^|\s)-?sort:", query, re.I):
        query += " sort:updated-desc"
    unreachable = SearchFailed("Couldn't reach %s" % host)
    try:
        out = gh(["api", "graphql", "--hostname", host, "-f", "query=" + SEARCH, "-f", "q=" + query], token)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        raise unreachable
    except subprocess.CalledProcessError as e:
        message = (e.stderr or "").replace(token, "***").strip()
        if any(m in message for m in UNREACHABLE):
            raise unreachable
        raise SearchFailed("Search failed: %s" % (re.sub(r"^gh: ", "", message) or "gh exited with status %d" % e.returncode))
    try:
        body = json.loads(out)
    except ValueError:
        raise SearchFailed("Search failed: GitHub's answer was not JSON")
    if not isinstance(body, dict):
        raise SearchFailed("Search failed: GitHub's answer was not understood")
    if body.get("errors"):
        raise SearchFailed("Search failed: %s" % (body["errors"][0].get("message") or "unknown error"))
    try:
        nodes = body["data"]["search"]["nodes"]
    except (KeyError, TypeError):
        raise SearchFailed("Search failed: GitHub's answer was not understood")
    return [o for o in map(to_order, nodes) if o][:MAX_ORDERS]


def to_order(node):
    try:
        repo = node["repository"]
        labels = {l["name"].lower() for l in node["labels"]["nodes"]}
        return {"id": "%s#%d" % (repo["nameWithOwner"], node["number"]), "title": node["title"],
                "text": node["url"], "url": node["url"], "repo": repo["url"],
                "hot": bool(labels & hot_labels)}
    except (KeyError, TypeError, AttributeError):  # a node of another type is empty
        return None


def refresh():
    """Search every panel and tell Admiral about the ones that changed."""
    global auth_message
    try:
        token = get_token()
    except SignInNeeded as e:
        with state_lock:
            fresh = auth_message != str(e)
            auth_message = str(e)
        if fresh:
            notify("auth.required", {"message": str(e)})
        return
    except SearchFailed as e:  # the token call hung: keep the sign-in state
        for panel in panels:
            store(panel, None, str(e))
        return
    with state_lock:
        was_signed_out = auth_message is not None
        auth_message = None
    if was_signed_out:
        notify("auth.ok")
    for panel in panels:
        try:
            store(panel, search(panel["query"], token), None)
        except SearchFailed as e:
            store(panel, None, str(e))
        except Exception:  # one odd answer must not stop the other panels
            store(panel, None, "Search failed: unexpected response")


def store(panel, orders, error):
    """Keep a panel's result (its last orders when there are none) and tell
    Admiral if the panel changed."""
    with state_lock:
        entry = {"orders": cache[panel["id"]]["orders"] if orders is None else orders, "error": error}
        changed = cache[panel["id"]] != entry
        cache[panel["id"]] = entry
    if changed:
        notify("orders.changed", {"panel": panel["id"]})


def work():
    """Search at start, on orders.refresh and every refresh_minutes. gh never
    runs on the thread that reads Admiral's requests."""
    while True:
        try:
            refresh()
        except Exception as e:
            print("github plugin: refresh failed: %s" % type(e).__name__, file=sys.stderr)
        wake.wait(refresh_minutes * 60)
        wake.clear()


def initialize(params):
    global host, account, panels, hot_labels, refresh_minutes
    settings = params.get("settings")
    settings = settings if isinstance(settings, dict) else {}
    host = text_setting(settings, "host", DEFAULT_HOST)
    account = text_setting(settings, "account", None)
    panels = read_panels(settings.get("panels"))
    hot_labels = read_hot_labels(settings.get("hotLabels"))
    refresh_minutes = read_minutes(settings.get("refreshMinutes"))
    cache.update({p["id"]: {"orders": [], "error": None} for p in panels})
    return {"protocolVersion": PROTOCOL_VERSION, "capabilities": ["refresh"], "settingsSchema": SETTINGS_SCHEMA}


def orders_list(params):
    with state_lock:
        entry = cache.get(params.get("panel"))
    if entry is None:
        raise RpcError(INVALID_PARAMS, "no such panel")
    if entry["error"]:
        raise RpcError(-32000, entry["error"])
    return entry["orders"]


def orders_refresh(params):
    wake.set()
    return {}


METHODS = {
    "initialize": initialize,
    "panels.list": lambda params: [{"id": p["id"], "title": p["title"]} for p in panels],
    "orders.list": orders_list,
    "orders.refresh": orders_refresh,
    "orders.launched": lambda params: {},  # Admiral hides the order while its ship is at sea
}


def handle(msg):
    method = msg.get("method")
    if "id" not in msg:
        return  # a notification from Admiral; this plugin has no use for one
    try:
        fn = METHODS.get(method)
        if fn is None:
            raise RpcError(METHOD_NOT_FOUND, "no such method: %s" % method)
        send({"jsonrpc": "2.0", "id": msg["id"], "result": fn(msg.get("params") or {})})
    except RpcError as e:
        send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": e.code, "message": e.message}})
    except Exception as e:
        send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32000, "message": str(e)}})
    if method == "panels.list" and not worker.is_alive():
        worker.start()  # only now may the plugin send notifications


worker = threading.Thread(target=work, daemon=True)


def main():
    for line in sys.stdin:
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        if isinstance(msg, dict):
            handle(msg)


main()
