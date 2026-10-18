#!/usr/bin/env python3
"""The freeform order source: a list of orders you type yourself.

It speaks Admiral's order source protocol (docs/plugins.md) and is its
reference plugin. One panel, "Orders". An order is multi-line text; its title
is the first non-empty line. The list lives in orders.json in the data
directory Admiral hands over. A launched order is deleted.
"""
import json
import os
import secrets
import sys

PROTOCOL_VERSION = 1
PANEL = {"id": "orders", "title": "Orders"}
MAX_TEXT_BYTES = 12000

INVALID_PARAMS = -32602
METHOD_NOT_FOUND = -32601

data_dir = None


class RpcError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def store_path():
    return os.path.join(data_dir, "orders.json")


def load():
    try:
        with open(store_path(), encoding="utf-8") as f:
            orders = json.load(f)
    except FileNotFoundError:
        return []
    if not isinstance(orders, list):
        raise RpcError(-32000, "orders.json is not a list")
    return orders


def save(orders):
    """Write the list so a reader sees the old file or the new one."""
    tmp = store_path() + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(orders, f, indent=2)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, store_path())


def title_of(text):
    for line in text.splitlines():
        if line.strip():
            return line.strip()
    return ""


def check_text(params):
    text = params.get("text")
    if not isinstance(text, str):
        raise RpcError(INVALID_PARAMS, "text must be a string")
    if not text.strip():
        raise RpcError(INVALID_PARAMS, "An order cannot be empty")
    if len(text.encode("utf-8")) > MAX_TEXT_BYTES:
        raise RpcError(INVALID_PARAMS, "An order can hold at most %d bytes" % MAX_TEXT_BYTES)
    return text


def check_panel(params):
    if params.get("panel") != PANEL["id"]:
        raise RpcError(INVALID_PARAMS, "no such panel")


def changed():
    send({"jsonrpc": "2.0", "method": "orders.changed", "params": {"panel": PANEL["id"]}})


def initialize(params):
    global data_dir
    data_dir = params["dataDir"]
    os.makedirs(data_dir, exist_ok=True)
    return {"protocolVersion": PROTOCOL_VERSION, "capabilities": ["add", "edit", "remove"],
            "settingsSchema": {"type": "object", "properties": {}, "additionalProperties": False}}


def orders_list(params):
    check_panel(params)
    return [{"id": o["id"], "title": title_of(o["text"]), "text": o["text"]} for o in load()]


def orders_add(params):
    check_panel(params)
    text = check_text(params)
    orders = load()
    order = {"id": secrets.token_hex(6), "text": text}
    orders.append(order)
    save(orders)
    changed()
    return {"id": order["id"]}


def orders_edit(params):
    check_panel(params)
    text = check_text(params)
    orders = load()
    for o in orders:
        if o["id"] == params.get("id"):
            o["text"] = text
            save(orders)
            changed()
            return {}
    raise RpcError(INVALID_PARAMS, "no such order")


def orders_remove(params):
    check_panel(params)
    orders = load()
    kept = [o for o in orders if o["id"] != params.get("id")]
    if len(kept) == len(orders):
        raise RpcError(INVALID_PARAMS, "no such order")
    save(kept)
    changed()
    return {}


def orders_launched(params):
    orders = load()
    kept = [o for o in orders if o["id"] != params.get("id")]
    if len(kept) != len(orders):
        save(kept)
        changed()
    return {}


METHODS = {
    "initialize": initialize,
    "panels.list": lambda params: [PANEL],
    "orders.list": orders_list,
    "orders.refresh": lambda params: {},
    "orders.add": orders_add,
    "orders.edit": orders_edit,
    "orders.remove": orders_remove,
    "orders.launched": orders_launched,
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
    except Exception as e:  # a broken orders.json, a full disk
        send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32000, "message": str(e)}})


def main():
    for line in sys.stdin:
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        if isinstance(msg, dict):
            handle(msg)


main()
