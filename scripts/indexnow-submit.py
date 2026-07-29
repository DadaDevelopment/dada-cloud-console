#!/usr/bin/env python3
"""Push URLs to the IndexNow endpoints so Yandex and Bing recrawl on demand.

Google ignores IndexNow entirely and retired its own sitemap ping endpoint in
2023, so nothing here reaches it -- claim the Search Console property instead.

By default the whole sitemap is submitted, which is what a weekly run wants.
Pass paths to submit a subset after a targeted change.

Usage:
    python3 scripts/indexnow-submit.py
    python3 scripts/indexnow-submit.py /hosting-discord-bot /en/hosting-discord-bot
"""

from __future__ import annotations

import json
import re
import sys
import urllib.error
import urllib.request

HOST = "cloud.dada-tuda.ru"
KEY = "2d5274e31d2e27fa515ff87cf799e650"
SITEMAP = f"https://{HOST}/sitemap.xml"
KEY_LOCATION = f"https://{HOST}/{KEY}.txt"
ENDPOINTS = [
    ("yandex", "https://yandex.com/indexnow"),
    ("bing", "https://www.bing.com/indexnow"),
]


def fetch(url: str, timeout: int = 30) -> bytes:
    with urllib.request.urlopen(url, timeout=timeout) as resp:
        return resp.read()


def sitemap_urls() -> list[str]:
    body = fetch(SITEMAP).decode("utf-8")
    return re.findall(r"<loc>([^<]+)</loc>", body)


def verify_key() -> None:
    served = fetch(KEY_LOCATION).decode("utf-8").strip()
    if served != KEY:
        raise SystemExit(f"key file at {KEY_LOCATION} serves {served!r}, expected {KEY!r}")


def submit(name: str, endpoint: str, urls: list[str]) -> None:
    payload = json.dumps(
        {"host": HOST, "key": KEY, "keyLocation": KEY_LOCATION, "urlList": urls}
    ).encode("utf-8")
    req = urllib.request.Request(
        endpoint, data=payload, headers={"Content-Type": "application/json; charset=utf-8"}
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            print(f"{name:8s} {resp.status} {resp.reason} ({len(urls)} urls)")
    except urllib.error.HTTPError as exc:
        print(f"{name:8s} {exc.code} {exc.reason} :: {exc.read()[:200]!r}")


def main() -> int:
    verify_key()
    args = sys.argv[1:]
    if args:
        urls = [a if a.startswith("http") else f"https://{HOST}{a}" for a in args]
    else:
        urls = sitemap_urls()
    if not urls:
        raise SystemExit("nothing to submit")
    print(f"submitting {len(urls)} urls")
    for name, endpoint in ENDPOINTS:
        submit(name, endpoint, urls)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
