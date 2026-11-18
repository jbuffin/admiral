"""Prints one line per case under OUT: the result, and each permission event's identifiers,
judge recommendation, outcome and decision source. usage: python3 -I summarize.py <out dir>"""
import json
import os
import sys

out = sys.argv[1]
for name in sorted(os.listdir(out)):
    d = os.path.join(out, name)
    result = open(os.path.join(d, "result.txt")).read().strip()
    lines = []
    for raw in open(os.path.join(d, "events.jsonl")):
        try:
            e = json.loads(raw)
        except ValueError:
            continue
        t, data = e.get("type", ""), e.get("data", {})
        if t == "permission.requested":
            pr = data.get("promptRequest") or {}
            req = data.get("permissionRequest") or {}
            aa = pr.get("assistedApproval") or {}
            lines.append(f"requested kind={req.get('kind')} ids={pr.get('commandIdentifiers')} "
                         f"paths={req.get('possiblePaths')} urls={req.get('possibleUrls')} judge={aa.get('recommendation')}")
        elif t == "permission.completed":
            lines.append(f"completed result={(data.get('result') or {}).get('kind')} source={data.get('decisionSource')}")
        elif t == "tool.execution_start":
            args = data.get("arguments") or {}
            lines.append(f"tool {data.get('toolName')} {json.dumps(args)[:120]}")
    print(f"{name}: {result}")
    for l in lines:
        print("    " + l)
