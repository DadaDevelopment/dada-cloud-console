"""Pull configured feeds into ``ai_news``.

Runs as a CronJob, not inside the MCP server: a feed being slow or dead must
never make a tool call slow or dead. Stdlib only (urllib + ElementTree), so
the ingest image needs nothing the runtime image does not already have.

Both RSS 2.0 and Atom are handled by ``parse_feed``, which is pure and takes
bytes, so the parser is unit-testable against fixtures without network.
"""

import argparse
import asyncio
import hashlib
import html
import re
import sys
import time
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET
from datetime import datetime, timedelta, timezone
from email.utils import parsedate_to_datetime

import sources

_TAG_RE = re.compile(r"<[^>]+>")
_WS_RE = re.compile(r"\s+")
_USER_AGENT = "tg-vibecoder-ingest/1.0 (+https://dada-tuda.ru)"
_SUMMARY_LIMIT = 600


def _text(node) -> str:
    if node is None:
        return ""
    return "".join(node.itertext()).strip()


def _clean(raw: str) -> str:
    return _WS_RE.sub(" ", html.unescape(_TAG_RE.sub(" ", raw))).strip()


def _parse_date(raw: str):
    raw = raw.strip()
    if not raw:
        return None
    try:
        parsed = parsedate_to_datetime(raw)
    except (TypeError, ValueError):
        try:
            parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
        except ValueError:
            return None
    if parsed is None:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def _atom_link(entry) -> str:
    for link in entry.findall("{http://www.w3.org/2005/Atom}link"):
        rel = link.get("rel", "alternate")
        if rel == "alternate" and link.get("href"):
            return link.get("href")
    for link in entry.findall("{http://www.w3.org/2005/Atom}link"):
        if link.get("href"):
            return link.get("href")
    return ""


def auto_tags(title: str, summary: str) -> list[str]:
    """Tag an item by the words a reader would search for, not by source."""
    haystack = f"{title} {summary}".lower()
    return [tag for tag, needles in sources.KEYWORD_TAGS.items() if any(n in haystack for n in needles)]


def parse_feed(raw: bytes, source_name: str, base_tags: list[str]) -> list[dict]:
    """Turn RSS 2.0 or Atom bytes into ``ai_news`` row dicts."""
    try:
        root = ET.fromstring(raw)
    except ET.ParseError:
        return []
    atom = "{http://www.w3.org/2005/Atom}"
    entries = root.findall(f".//{atom}entry") or root.findall(".//item")
    out = []
    for entry in entries:
        is_atom = entry.tag.startswith(atom)
        if is_atom:
            title = _clean(_text(entry.find(f"{atom}title")))
            url = _atom_link(entry)
            summary = _clean(
                _text(entry.find(f"{atom}summary")) or _text(entry.find(f"{atom}content"))
            )
            published = _parse_date(
                _text(entry.find(f"{atom}published")) or _text(entry.find(f"{atom}updated"))
            )
            guid = _text(entry.find(f"{atom}id"))
        else:
            title = _clean(_text(entry.find("title")))
            url = _text(entry.find("link"))
            summary = _clean(_text(entry.find("description")))
            published = _parse_date(_text(entry.find("pubDate")))
            guid = _text(entry.find("guid"))
        if not title:
            continue
        key = guid or url or title
        out.append(
            {
                "fingerprint": hashlib.sha256(f"{source_name}|{key}".encode("utf-8")).hexdigest(),
                "source": source_name,
                "title": title[:400],
                "url": url[:1000],
                "summary": summary[:_SUMMARY_LIMIT],
                "tags": sorted(set(base_tags) | set(auto_tags(title, summary))),
                "published_at": published,
            }
        )
    return out


FETCH_ATTEMPTS = 3


def fetch(url: str, timeout: float, attempts: int = FETCH_ATTEMPTS) -> bytes:
    """Fetch one feed, retrying a timeout or a 5xx.

    A read timeout is not evidence that a source is dead: habr answered fine
    from a laptop and timed out from the cluster in the same hour. Without a
    retry that single flake silently costs the run a whole source until the
    next cron tick three hours later. A 4xx is a verdict, not a flake, so it
    is not retried.
    """
    request = urllib.request.Request(url, headers={"User-Agent": _USER_AGENT})
    last: Exception | None = None
    for attempt in range(attempts):
        try:
            with urllib.request.urlopen(request, timeout=timeout) as resp:
                return resp.read()
        except urllib.error.HTTPError as exc:
            if exc.code < 500:
                raise
            last = exc
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            last = exc
        if attempt + 1 < attempts:
            time.sleep(2 ** attempt)
    raise last


def collect(feeds: list[dict], timeout: float, max_age_days: int) -> tuple[list[dict], list[dict]]:
    """Fetch every feed. Returns (items, per-source report).

    A dead feed produces an ``error`` report row and zero items; it never
    raises, because one rotten source must not cost the run every other one.
    """
    cutoff = datetime.now(timezone.utc) - timedelta(days=max_age_days)
    items: list[dict] = []
    report: list[dict] = []
    for feed in feeds:
        try:
            parsed = parse_feed(fetch(feed["url"], timeout), feed["name"], feed.get("tags", []))
        except (urllib.error.URLError, ET.ParseError, OSError, ValueError) as exc:
            report.append({"source": feed["name"], "status": "error", "detail": f"{type(exc).__name__}: {exc}"})
            continue
        fresh = [i for i in parsed if i["published_at"] is None or i["published_at"] >= cutoff]
        items.extend(fresh)
        report.append({"source": feed["name"], "status": "ok", "parsed": len(parsed), "fresh": len(fresh)})
    return items, report


async def run(timeout: float, max_age_days: int, dry_run: bool) -> int:
    items, report = collect(sources.FEEDS, timeout, max_age_days)
    for row in report:
        if row["status"] == "ok":
            print(f"{row['source']}: ok parsed={row['parsed']} fresh={row['fresh']}", file=sys.stderr)
        else:
            print(f"{row['source']}: error {row['detail']}", file=sys.stderr)
    if dry_run:
        print(f"dry-run: {len(items)} items, 0 written")
        return 0
    import storage

    written = await storage.upsert_news(items)
    print(f"ingest: {len(items)} items seen, {written} new rows")
    ok_sources = sum(1 for r in report if r["status"] == "ok")
    return 0 if ok_sources else 1


def main() -> int:
    parser = argparse.ArgumentParser(description="Ingest AI/tooling feeds into ai_news")
    parser.add_argument("--timeout", type=float, default=20.0)
    parser.add_argument("--max-age-days", type=int, default=45)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    return asyncio.run(run(args.timeout, args.max_age_days, args.dry_run))


if __name__ == "__main__":
    raise SystemExit(main())
