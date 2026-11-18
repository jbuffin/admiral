"""Cuts each case's events.jsonl down to the permission and tool events, in place, and drops
the full pane capture to its last 30 lines. usage: python3 -I trim.py <out dir>"""
import json
import os
import sys

KEEP = ("permission.requested", "permission.completed", "tool.execution_start", "tool.execution_complete")
out = sys.argv[1]
for name in sorted(os.listdir(out)):
    d = os.path.join(out, name)
    p = os.path.join(d, "events.jsonl")
    kept = []
    for raw in open(p):
        try:
            e = json.loads(raw)
        except ValueError:
            continue
        if e.get("type") in KEEP:
            kept.append(json.dumps(e, separators=(",", ":")))
    open(p, "w").write("\n".join(kept) + "\n")
    pane = os.path.join(d, "pane.txt")
    lines = [l.rstrip() for l in open(pane)]
    while lines and not lines[-1]:
        lines.pop()
    open(pane, "w").write("\n".join(lines[-30:]) + "\n")
