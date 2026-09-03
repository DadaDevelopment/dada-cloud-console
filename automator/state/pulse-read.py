#!/usr/bin/env python3
import sys, json

mode = sys.argv[1]
raw = sys.stdin.read()
try:
    d = json.loads(raw)
except Exception:
    sys.exit(3)

if mode == "field":
    path = sys.argv[2]
    cur = d
    for part in path.split("."):
        if isinstance(cur, dict):
            cur = cur.get(part)
        elif isinstance(cur, list) and part.isdigit():
            cur = cur[int(part)] if int(part) < len(cur) else None
        else:
            cur = None
        if cur is None:
            break
    if cur is None:
        print("")
    elif isinstance(cur, dict):
        if "stale_apps" in cur:
            print(f"stale_apps={cur.get('stale_apps')} newest_sync_age_s={cur.get('newest_sync_age_seconds')} blind={cur.get('blind')}")
        else:
            print(json.dumps(cur, ensure_ascii=False))
    elif isinstance(cur, list):
        print(json.dumps(cur, ensure_ascii=False))
    else:
        print(cur)
elif mode == "count":
    path = sys.argv[2]
    cur = d
    for part in path.split("."):
        if isinstance(cur, dict):
            cur = cur.get(part)
        elif isinstance(cur, list) and part.isdigit():
            cur = cur[int(part)] if int(part) < len(cur) else None
        else:
            cur = None
        if cur is None:
            break
    if isinstance(cur, dict):
        c = cur.get("count")
        print(c if isinstance(c, int) else len(cur))
    elif isinstance(cur, list):
        print(len(cur))
    else:
        print(0)
elif mode == "names":
    path = sys.argv[2]
    cur = d
    for part in path.split("."):
        if isinstance(cur, dict):
            cur = cur.get(part)
        elif isinstance(cur, list) and part.isdigit():
            cur = cur[int(part)] if int(part) < len(cur) else None
        else:
            cur = None
        if cur is None:
            break
    if isinstance(cur, list):
        for item in cur:
            if isinstance(item, dict):
                if "stale_apps" in item:
                    print(f"    stale_apps={item.get('stale_apps')} newest_sync_age_s={item.get('newest_sync_age_seconds')} blind={item.get('blind')}")
                    continue
                for k in ("name", "app_name", "domain", "operation_id", "build_id", "id"):
                    if item.get(k):
                        print("    -", item[k])
                        break
                else:
                    print("    -", json.dumps(item, ensure_ascii=False)[:120])
            else:
                print("    -", item)
elif mode == "counters":
    c = d.get("counters") or {}
    for k, v in c.items():
        print(f"  {k}: {v}")
elif mode == "errors":
    e = d.get("errors") or []
    print(len(e))
    for line in e:
        print("  -", line)
