"""Backfill ``channel_posts`` from a public channel's web preview.

The Bot API cannot read a channel's history: a bot only ever receives posts
made after it joined. The public ``t.me/s/<channel>`` view is the only route
to what the channel already published, and it paginates backwards through
``?before=<message_id>``.

Parsing is regex over the preview markup rather than an HTML dependency; the
extraction is narrow (post id, text block, timestamp) and ``parse_page`` is
pure so it is testable against a saved fixture. A markup change shows up as
zero parsed posts, which the CLI reports rather than silently succeeding.
"""

import argparse
import asyncio
import html
import re
import sys
import urllib.request
from datetime import datetime, timezone

_USER_AGENT = "Mozilla/5.0 (compatible; tg-vibecoder-ingest/1.0)"
_TEXT_BLOCK_RE = re.compile(
    r'data-post="[^"/]+/(?P<id>\d+)"(?P<body>.*?)(?=data-post="[^"/]+/\d+"|\Z)', re.S
)
_MESSAGE_TEXT_RE = re.compile(
    r'<div class="tgme_widget_message_text[^"]*"[^>]*>(?P<text>.*?)</div>', re.S
)
_TIME_RE = re.compile(r'<time[^>]+datetime="(?P<time>[^"]+)"')
_TAG_RE = re.compile(r"<br\s*/?>|</p>", re.I)
_STRIP_RE = re.compile(r"<[^>]+>")
_WS_RE = re.compile(r"[ \t]+")


def _clean(raw: str) -> str:
    text = _TAG_RE.sub("\n", raw)
    text = _STRIP_RE.sub("", text)
    text = html.unescape(text)
    lines = [_WS_RE.sub(" ", line).strip() for line in text.splitlines()]
    return "\n".join(line for line in lines if line).strip()


def parse_page(raw_html: str) -> list[dict]:
    """Extract posts from one ``t.me/s/<channel>`` page."""
    posts = []
    for block in _TEXT_BLOCK_RE.finditer(raw_html):
        body = block.group("body")
        text_match = _MESSAGE_TEXT_RE.search(body)
        if not text_match:
            continue
        text = _clean(text_match.group("text"))
        if not text:
            continue
        posted_at = None
        time_match = _TIME_RE.search(body)
        if time_match:
            try:
                posted_at = datetime.fromisoformat(time_match.group("time").replace("Z", "+00:00"))
                posted_at = posted_at.astimezone(timezone.utc)
            except ValueError:
                posted_at = None
        posts.append({"message_id": int(block.group("id")), "text": text, "posted_at": posted_at})
    return posts


def fetch_page(channel: str, before: int | None, timeout: float) -> str:
    url = f"https://t.me/s/{channel}"
    if before:
        url = f"{url}?before={before}"
    request = urllib.request.Request(url, headers={"User-Agent": _USER_AGENT})
    with urllib.request.urlopen(request, timeout=timeout) as resp:
        return resp.read().decode("utf-8", errors="replace")


def crawl(channel: str, pages: int, timeout: float) -> list[dict]:
    """Walk backwards through the preview, newest page first."""
    seen: dict[int, dict] = {}
    before: int | None = None
    for _ in range(pages):
        page = fetch_page(channel, before, timeout)
        posts = parse_page(page)
        if not posts:
            break
        for post in posts:
            seen.setdefault(post["message_id"], post)
        oldest = min(post["message_id"] for post in posts)
        if before is not None and oldest >= before:
            break
        before = oldest
    return sorted(seen.values(), key=lambda p: p["message_id"])


async def run(channel: str, pages: int, timeout: float, dry_run: bool) -> int:
    posts = crawl(channel, pages, timeout)
    print(f"parsed {len(posts)} posts from t.me/s/{channel}", file=sys.stderr)
    if not posts:
        print("no posts parsed: markup changed, channel private, or network blocked", file=sys.stderr)
        return 1
    if dry_run:
        for post in posts[-3:]:
            print(f"--- {post['message_id']} {post['posted_at']}\n{post['text'][:200]}")
        return 0
    import storage

    written = await storage.upsert_channel_posts(posts)
    print(f"channel ingest: {len(posts)} seen, {written} new rows")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="Backfill channel_posts from the public channel preview")
    parser.add_argument("channel", help="channel username without @")
    parser.add_argument("--pages", type=int, default=10)
    parser.add_argument("--timeout", type=float, default=20.0)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    return asyncio.run(run(args.channel, args.pages, args.timeout, args.dry_run))


if __name__ == "__main__":
    raise SystemExit(main())
