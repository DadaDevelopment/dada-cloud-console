#!/usr/bin/env python3
"""Weekly SEO pull for cloud.dada-tuda.ru.

Pulls the authoritative numbers from the two Yandex APIs and writes a machine
readable snapshot plus a human readable diff against the previous run.

Sources:
  * Yandex Webmaster v4  -- indexation, per-query shows/clicks/position,
    per-URL search presence, crawl errors.
  * Yandex Metrika stat v1 -- sessions and goal conversions per landing page
    and per traffic source, so search impressions can be tied to signups.

Auth: a single OAuth token with the ``metrika:read`` and ``webmaster:hostinfo``
scopes, taken from the ``YANDEX_OAUTH_TOKEN`` environment variable.

Usage:
    YANDEX_OAUTH_TOKEN=... python3 scripts/seo-weekly.py [--days 7] [--out DIR]
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

WEBMASTER = "https://api.webmaster.yandex.net/v4"
METRIKA_STAT = "https://api-metrika.yandex.net/stat/v1/data"
METRIKA_MGMT = "https://api-metrika.yandex.net/management/v1"
HOST_MATCH = "cloud.dada-tuda.ru"
COUNTER_ID = os.environ.get("YM_COUNTER_ID", "110158915")
DEFAULT_OUT = "tasks/seo"


class ApiError(RuntimeError):
    pass


def get(url: str, token: str, params: dict | None = None) -> dict:
    if params:
        url = f"{url}?{urllib.parse.urlencode(params, doseq=True)}"
    req = urllib.request.Request(url, headers={"Authorization": f"OAuth {token}"})
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "ignore")
        raise ApiError(f"{exc.code} {url}\n{body}") from exc


def webmaster_host(token: str) -> tuple[str, str]:
    user_id = get(f"{WEBMASTER}/user", token)["user_id"]
    hosts = get(f"{WEBMASTER}/user/{user_id}/hosts", token)["hosts"]
    for host in hosts:
        if HOST_MATCH in host["ascii_host_url"]:
            return str(user_id), host["host_id"]
    known = ", ".join(h["ascii_host_url"] for h in hosts) or "<none>"
    raise ApiError(f"{HOST_MATCH} not found in Webmaster hosts: {known}")


def collect_webmaster(token: str, date_from: str, date_to: str) -> dict:
    user_id, host_id = webmaster_host(token)
    base = f"{WEBMASTER}/user/{user_id}/hosts/{host_id}"
    out: dict = {"host_id": host_id}

    out["summary"] = get(f"{base}/summary", token)

    out["queries"] = get(
        f"{base}/search-queries/popular",
        token,
        {
            "order_by": "TOTAL_SHOWS",
            "query_indicator": ["TOTAL_SHOWS", "TOTAL_CLICKS", "AVG_SHOW_POSITION", "AVG_CLICK_POSITION"],
            "date_from": date_from,
            "date_to": date_to,
            "limit": 500,
        },
    )

    out["queries_history"] = get(
        f"{base}/search-queries/history/all",
        token,
        {"query_indicator": ["TOTAL_SHOWS", "TOTAL_CLICKS"], "date_from": date_from, "date_to": date_to},
    )

    out["indexing_history"] = get(
        f"{base}/search-urls/in-search/history",
        token,
        {"date_from": date_from, "date_to": date_to},
    )

    out["in_search_samples"] = get(f"{base}/search-urls/in-search/samples", token, {"limit": 100})
    out["excluded_samples"] = get(f"{base}/search-urls/excluded/samples", token, {"limit": 100})
    out["diagnostics"] = get(f"{base}/diagnostics", token)
    return out


def metrika_report(token: str, dimensions: list[str], metrics: list[str], date1: str, date2: str,
                   filters: str | None = None, limit: int = 200) -> dict:
    params = {
        "ids": COUNTER_ID,
        "dimensions": ",".join(dimensions),
        "metrics": ",".join(metrics),
        "date1": date1,
        "date2": date2,
        "limit": limit,
        "accuracy": "full",
    }
    if filters:
        params["filters"] = filters
    return get(METRIKA_STAT, token, params)


def collect_metrika(token: str, date1: str, date2: str) -> dict:
    out: dict = {"counter_id": COUNTER_ID}
    goals = get(f"{METRIKA_MGMT}/counter/{COUNTER_ID}/goals", token).get("goals", [])
    out["goals"] = [{"id": g["id"], "name": g["name"]} for g in goals]

    out["landing_pages"] = metrika_report(
        token,
        ["ym:s:startURLPath"],
        ["ym:s:visits", "ym:s:users", "ym:s:bounceRate", "ym:s:avgVisitDurationSeconds"],
        date1, date2,
    )

    out["search_landing_pages"] = metrika_report(
        token,
        ["ym:s:startURLPath"],
        ["ym:s:visits", "ym:s:users"],
        date1, date2,
        filters="ym:s:lastsignTrafficSource=='organic'",
    )

    out["sources"] = metrika_report(
        token,
        ["ym:s:lastsignTrafficSource", "ym:s:lastsignSearchEngine"],
        ["ym:s:visits", "ym:s:users"],
        date1, date2,
    )

    out["search_phrases"] = metrika_report(
        token,
        ["ym:s:lastsignSearchPhrase"],
        ["ym:s:visits", "ym:s:users"],
        date1, date2,
    )

    for goal in out["goals"]:
        gid = goal["id"]
        out[f"goal_{gid}_by_landing"] = metrika_report(
            token,
            ["ym:s:startURLPath"],
            [f"ym:s:goal{gid}reaches", f"ym:s:goal{gid}conversionRate"],
            date1, date2,
        )
    return out


def flat_queries(wm: dict) -> dict[str, dict]:
    rows = {}
    for item in wm.get("queries", {}).get("queries", []):
        text = item.get("query_text", "")
        ind = item.get("indicators", {})
        rows[text] = {
            "shows": ind.get("TOTAL_SHOWS", 0),
            "clicks": ind.get("TOTAL_CLICKS", 0),
            "pos": ind.get("AVG_SHOW_POSITION"),
        }
    return rows


def flat_landings(mk: dict, key: str = "landing_pages") -> dict[str, int]:
    rows = {}
    for row in mk.get(key, {}).get("data", []):
        path = row["dimensions"][0].get("name") or "(none)"
        rows[path] = row["metrics"][0]
    return rows


def render(snapshot: dict, prev: dict | None) -> str:
    wm, mk = snapshot["webmaster"], snapshot["metrika"]
    lines = [f"# SEO weekly -- {snapshot['date_to']} (window {snapshot['date_from']}..{snapshot['date_to']})", ""]

    searchable = wm.get("summary", {}).get("searchable_urls_count")
    excluded = wm.get("summary", {}).get("excluded_urls_count")
    sqi = wm.get("summary", {}).get("sqi")
    lines += ["## Index", f"- in search: {searchable}", f"- excluded: {excluded}", f"- SQI: {sqi}", ""]

    q = flat_queries(wm)
    prev_q = flat_queries(prev["webmaster"]) if prev else {}
    total_shows = sum(v["shows"] for v in q.values())
    total_clicks = sum(v["clicks"] for v in q.values())
    ctr = (100.0 * total_clicks / total_shows) if total_shows else 0.0
    lines += ["## Search demand", f"- shows: {total_shows}", f"- clicks: {total_clicks}", f"- CTR: {ctr:.1f}%", ""]

    lines += ["### Top queries", "", "| query | shows | clicks | avg pos | d shows |", "|---|---:|---:|---:|---:|"]
    for text, v in sorted(q.items(), key=lambda kv: -kv[1]["shows"])[:30]:
        delta = v["shows"] - prev_q.get(text, {}).get("shows", 0)
        pos = f"{v['pos']:.1f}" if isinstance(v["pos"], (int, float)) else "-"
        lines.append(f"| {text} | {v['shows']} | {v['clicks']} | {pos} | {delta:+d} |")
    lines.append("")

    if prev_q:
        new_q = [t for t in q if t not in prev_q]
        lost_q = [t for t in prev_q if t not in q]
        lines += ["### Query churn", f"- new: {', '.join(new_q[:20]) or '-'}", f"- lost: {', '.join(lost_q[:20]) or '-'}", ""]

    land = flat_landings(mk)
    organic = flat_landings(mk, "search_landing_pages")
    lines += ["## Traffic by landing", "", "| path | visits | organic visits |", "|---|---:|---:|"]
    for path, visits in sorted(land.items(), key=lambda kv: -kv[1])[:40]:
        lines.append(f"| {path} | {visits:.0f} | {organic.get(path, 0):.0f} |")
    lines.append("")

    for goal in mk.get("goals", []):
        gid, name = goal["id"], goal["name"]
        rows = mk.get(f"goal_{gid}_by_landing", {}).get("data", [])
        hits = [(r["dimensions"][0].get("name"), r["metrics"][0]) for r in rows if r["metrics"][0]]
        if not hits:
            continue
        lines += [f"### Goal: {name} (id {gid})", "", "| path | reaches |", "|---|---:|"]
        for path, n in sorted(hits, key=lambda kv: -kv[1])[:20]:
            lines.append(f"| {path} | {n:.0f} |")
        lines.append("")

    excluded_rows = wm.get("excluded_samples", {}).get("samples", [])
    if excluded_rows:
        lines += ["## Excluded from search (sample)", "", "| url | reason |", "|---|---|"]
        for s in excluded_rows[:30]:
            lines.append(f"| {s.get('url','')} | {s.get('status','')} |")
        lines.append("")

    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--days", type=int, default=7)
    ap.add_argument("--out", default=DEFAULT_OUT)
    args = ap.parse_args()

    token = os.environ.get("YANDEX_OAUTH_TOKEN")
    if not token:
        print("YANDEX_OAUTH_TOKEN is not set", file=sys.stderr)
        return 2

    date_to = dt.date.today()
    date_from = date_to - dt.timedelta(days=args.days)
    d1, d2 = date_from.isoformat(), date_to.isoformat()

    snapshot = {
        "date_from": d1,
        "date_to": d2,
        "webmaster": collect_webmaster(token, d1, d2),
        "metrika": collect_metrika(token, d1, d2),
    }

    os.makedirs(args.out, exist_ok=True)
    prev = None
    old = sorted(f for f in os.listdir(args.out) if f.endswith(".json"))
    if old:
        with open(os.path.join(args.out, old[-1]), encoding="utf-8") as fh:
            prev = json.load(fh)

    json_path = os.path.join(args.out, f"{d2}.json")
    md_path = os.path.join(args.out, f"{d2}.md")
    with open(json_path, "w", encoding="utf-8") as fh:
        json.dump(snapshot, fh, ensure_ascii=False, indent=2)
    with open(md_path, "w", encoding="utf-8") as fh:
        fh.write(render(snapshot, prev))

    print(f"wrote {json_path}")
    print(f"wrote {md_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
