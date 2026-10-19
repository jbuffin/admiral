#!/usr/bin/env python3
"""A misbehaving order source plugin for Admiral's server tests.

Everything it does is steered from outside through settings.flagdir, a
directory the test owns (the settings are the one input the protocol gives a
plugin). Settings, all optional except flagdir:

  flagdir          control and log directory
  capabilities     list answered by initialize (default ["refresh"])
  protocolVersion  number answered by initialize (default 1)
  title            title of the default panel "main"
  settingsSchema   schema answered by initialize (default: flagdir as a string)
  noSettingsSchema true: answer initialize without any settingsSchema

Files the plugin writes in flagdir:

  starts    one epoch-seconds float per line, one line per initialize received
  calls     one JSON line per request received: {"method", "params"}
  pid       its process id
  cwd       its working directory
  datadir   the dataDir initialize handed it (it also writes dataDir/marker)

Files the test writes in flagdir:

  orders.json   {"<panel id>": [order, ...]}, read on every orders.list
  panels.json   [{"id", "title"}], read on every panels.list (default one
                panel "main"); settings.panels, a list of {title, ...}, wins
                when it is set and lists one panel per entry, id = title
  crash-on-initialize / crash-on-panels
                exit with status 1 when that request arrives, for as long as
                the file exists
  hang-<method>   never answer that method (hang-initialize, hang-orders.list…)
  fail-orders.list-<panel id>
                orders.list for that panel answers with a JSON-RPC error whose
                message is the file's content, for as long as the file exists
  slow-initialize   file content is the seconds initialize waits before answering
  do-<command>  consumed once, content is the command's argument, the plugin:
                  do-crash            exits with status 1
                  do-auth-required    sends auth.required with the content as params
                  do-auth-ok          sends auth.ok
                  do-orders-changed   sends orders.changed (content: params, or none)
                  do-panels-changed   sends panels.changed
                  do-garbage          writes a line that is not JSON
                  do-oversize         writes a 17 MiB line of valid JSON
"""
import json
import os
import sys
import threading
import time

out_lock = threading.Lock()
flagdir = None
settings = {}


def emit(obj):
    with out_lock:
        sys.stdout.write(json.dumps(obj) + "\n")
        sys.stdout.flush()


def raw_emit(text):
    with out_lock:
        sys.stdout.write(text)
        sys.stdout.flush()


def path(name):
    return os.path.join(flagdir, name)


def flag(name):
    return os.path.exists(path(name))


def read_json(name, default):
    try:
        with open(path(name)) as f:
            return json.load(f)
    except (OSError, ValueError):
        return default


def append(name, line):
    with open(path(name), "a") as f:
        f.write(line + "\n")


def hang():
    while True:
        time.sleep(3600)


def control_loop():
    while True:
        time.sleep(0.1)
        for name in sorted(os.listdir(flagdir)):
            if not name.startswith("do-"):
                continue
            try:
                with open(path(name)) as f:
                    content = f.read().strip()
                os.remove(path(name))
            except OSError:
                continue
            cmd = name[3:]
            if cmd == "crash":
                os._exit(1)
            elif cmd == "auth-required":
                emit({"jsonrpc": "2.0", "method": "auth.required", "params": json.loads(content or "{}")})
            elif cmd == "auth-ok":
                emit({"jsonrpc": "2.0", "method": "auth.ok"})
            elif cmd == "orders-changed":
                note = {"jsonrpc": "2.0", "method": "orders.changed"}
                if content:
                    note["params"] = json.loads(content)
                emit(note)
            elif cmd == "panels-changed":
                emit({"jsonrpc": "2.0", "method": "panels.changed"})
            elif cmd == "garbage":
                raw_emit("this is not json\n")
            elif cmd == "oversize":
                # valid JSON, so only the size limit can be why it is dropped
                raw_emit('{"jsonrpc":"2.0","method":"orders.changed","params":{"pad":"'
                         + "x" * (17 * 1024 * 1024) + '"}}\n')


def handle(msg):
    global flagdir, settings
    method = msg.get("method")
    params = msg.get("params") or {}
    result = {}
    if method == "initialize":
        settings = params.get("settings") or {}
        flagdir = settings["flagdir"]
        os.makedirs(flagdir, exist_ok=True)
        append("starts", repr(time.time()))
        with open(path("pid"), "w") as f:
            f.write(str(os.getpid()))
        with open(path("cwd"), "w") as f:
            f.write(os.getcwd())
        with open(path("datadir"), "w") as f:
            f.write(str(params.get("dataDir")))
        try:
            with open(os.path.join(params["dataDir"], "marker"), "w") as f:
                f.write(flagdir)
        except (OSError, KeyError, TypeError):
            pass
        sys.stderr.write("testplugin: initialize received\n")
        sys.stderr.flush()
    if flagdir:
        append("calls", json.dumps({"method": method, "params": params}))
    if method == "initialize":
        if flag("crash-on-initialize"):
            os._exit(1)
        if flag("hang-initialize"):
            hang()
        if flag("slow-initialize"):
            with open(path("slow-initialize")) as f:
                time.sleep(float(f.read().strip() or "2"))
        threading.Thread(target=control_loop, daemon=True).start()
        result = {"protocolVersion": settings.get("protocolVersion", 1),
                  "capabilities": settings.get("capabilities", ["refresh"]),
                  "settingsSchema": settings.get("settingsSchema", {"type": "object", "properties": {"flagdir": {"type": "string"}}})}
        if settings.get("noSettingsSchema"):
            del result["settingsSchema"]
    elif flagdir and flag("hang-" + str(method)):
        hang()
    elif method == "panels.list":
        if flag("crash-on-panels"):
            os._exit(1)
        if settings.get("panels"):
            result = [{"id": p["title"], "title": p["title"]} for p in settings["panels"]]
        else:
            result = read_json("panels.json", [{"id": "main", "title": settings.get("title", "Test")}])
    elif method == "orders.list" and flagdir and flag("fail-orders.list-" + str(params.get("panel"))):
        with open(path("fail-orders.list-" + str(params.get("panel")))) as f:
            message = f.read().strip()
        emit({"jsonrpc": "2.0", "id": msg.get("id"), "error": {"code": -32000, "message": message}})
        return
    elif method == "orders.list":
        result = read_json("orders.json", {}).get(params.get("panel"), [])
    elif method == "orders.add":
        result = {"id": "added-1"}
    elif method in ("orders.refresh", "orders.edit", "orders.remove", "orders.launched"):
        result = {}
    else:
        emit({"jsonrpc": "2.0", "id": msg.get("id"), "error": {"code": -32601, "message": "no such method"}})
        return
    if "id" in msg:
        emit({"jsonrpc": "2.0", "id": msg["id"], "result": result})


def main():
    for line in sys.stdin:
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        if msg.get("method") == "initialize":
            handle(msg)  # nothing else is answered before it
        else:
            threading.Thread(target=handle, args=(msg,), daemon=True).start()


main()
