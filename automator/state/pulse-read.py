#!/usr/bin/env python3
import sys, json

mode = sys.argv[1]
raw = sys.stdin.read()
try:
    d = json.loads(raw)
except Exception:
    sys.exit(3)

def _health_mark(kind, age_seconds):
    """Label an unhealthy entry by how much it deserves alarm.

    The snapshot is one instant, and an Argo rollout puts every replaced pod in
    Pending for a few seconds; age is what separates a rollout from an incident.
    A Deployment-level ReplicasNotReady flag survives long after the pods
    recover, so the pod rows, not the workload rows, are the signal to act on.
    """
    if kind == "Pod" and (age_seconds or 0) < 300:
        return "  [свежий, вероятно раскатка - сверь живьём]"
    if kind == "Deployment":
        return "  [воркладный флаг, часто протухший - верь подам]"
    return ""


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
elif mode == "health":
    ph = d.get("overview", {}).get("platform_health") or {}
    if not ph.get("observed"):
        print("  НЕ НАБЛЮДАЛОСЬ - это не 'здорово', сборщик не смотрел")
    else:
        bad = ph.get("unhealthy") or []
        print(f"  подов {ph.get('pods_total')} / воркладов {ph.get('workloads_total')}"
              f" в {','.join(ph.get('namespaces') or [])}, нездоровых {len(bad)}")
        for item in bad:
            print(f"    - {item.get('namespace')}/{item.get('name')} kind={item.get('kind')}"
                  f" phase={item.get('phase')} ready={item.get('ready')}"
                  f" restarts={item.get('restarts')} reason={item.get('reason')}"
                  f" age_s={item.get('age_seconds')}"
                  f"{_health_mark(item.get('kind'), item.get('age_seconds'))}")
elif mode == "urls":
    lu = d.get("overview", {}).get("live_urls") or {}
    if not lu:
        print("  нет данных в снимке")
    else:
        print(f"  проверено {lu.get('checked')}: ok={lu.get('ok')} мертвы={lu.get('dead')}"
              f" ответило_приложение={lu.get('app_responded')} никогда_не_отвечали={lu.get('never_http')}")
        for item in lu.get("dead_apps") or []:
            print(f"    - {item.get('name')} {item.get('hostname')} http={item.get('http_status')}"
                  f" ({item.get('http_reason')}) владелец={item.get('owner_email')}")
elif mode == "counters":
    c = d.get("counters") or {}
    for k, v in c.items():
        print(f"  {k}: {v}")
elif mode == "errors":
    e = d.get("errors") or []
    print(len(e))
    for line in e:
        print("  -", line)
