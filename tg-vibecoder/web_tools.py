"""Best-effort web search and page fetch for the vibecoder agent.

Ported from reels-task-tools (core/agent/tools.py) where the same DDG ladder
has been answering in production since 2026-09: the lite endpoint parses with
three regexes, a query that finds nothing is retried shorter, and an endpoint
that stops answering is cooled down instead of stalling every turn.

These are static MCP tools, not manifests: they are capability, not data.
The persona rules still hold — a URL is a fact only after fetch opened it,
and a claim about the world cites what a tool returned, never memory.
"""

import asyncio
import html
import random
import re

import httpx

_SEARCH_RESULTS = 6
_SEARCH_ATTEMPTS = 3
_SEARCH_BACKOFF = 1.5
_SEARCH_DEADLINE = 20.0
_SEARCH_COOLDOWN = 120.0
_FETCH_LIMIT = 6000

_DDGS = [
    "https://lite.duckduckgo.com/lite/",
    "https://html.duckduckgo.com/html/",
]

_LINK_RE = re.compile(r'href="[^"]*\?uddg=([^"&]+)[^"]*"[^>]*>(.*?)</a>', re.S)
_SNIPPET_RE = re.compile(r"class=[\"']result-snippet[\"'][^>]*>(.*?)</td>", re.S)
_TAG_RE = re.compile(r"<[^>]+>")

_SEARCH_DOWN: dict[str, float] = {}
_SEARCH_GATE = asyncio.Semaphore(2)

_UA = "Mozilla/5.0 (compatible; tg-vibecoder/1.0; web-research)"


def _strip_html(raw: str) -> str:
    text = _TAG_RE.sub(" ", raw)
    text = html.unescape(text)
    lines = [ln.strip() for ln in text.splitlines()]
    return "\n".join(ln for ln in lines if ln).strip()


def _shorten(query: str) -> str:
    words = [w for w in query.split() if w]
    return " ".join(words[:6])


async def _ddg_once(client, base_url: str, query: str) -> list[dict]:
    from urllib.parse import unquote

    resp = await client.get(base_url, params={"q": query})
    resp.raise_for_status()
    body = resp.text
    snippets = [_strip_html(s).strip() for s in _SNIPPET_RE.findall(body)]
    results: list[dict] = []
    seen: set[str] = set()
    for encoded, title in _LINK_RE.findall(body):
        url = unquote(encoded)
        if not url.startswith("http") or url in seen:
            continue
        seen.add(url)
        idx = len(results)
        results.append({
            "url": url,
            "title": _strip_html(title).strip(),
            "snippet": snippets[idx] if idx < len(snippets) else "",
        })
        if len(results) >= _SEARCH_RESULTS:
            break
    return results


async def search_web(query: str) -> list[dict]:
    """Run the DDG ladder. Empty list means nothing found or engine down."""
    import time
    now = time.monotonic()
    if now < _SEARCH_DOWN.get("until", 0.0):
        return []

    deadline = now + _SEARCH_DEADLINE
    try:
        async with asyncio.timeout(_SEARCH_DEADLINE):
            async with _SEARCH_GATE:
                for attempt in range(_SEARCH_ATTEMPTS):
                    for base_url in _DDGS:
                        for candidate in (query, _shorten(query)):
                            if time.monotonic() > deadline:
                                _SEARCH_DOWN["until"] = time.monotonic() + _SEARCH_COOLDOWN
                                return []
                            try:
                                async with httpx.AsyncClient(
                                    timeout=12, follow_redirects=True,
                                    headers={"User-Agent": _UA},
                                ) as client:
                                    results = await _ddg_once(client, base_url, candidate)
                            except Exception:
                                continue
                            if results:
                                _SEARCH_DOWN["until"] = 0.0
                                return results
                    if attempt < _SEARCH_ATTEMPTS - 1:
                        await asyncio.sleep(_SEARCH_BACKOFF * (2 ** attempt) * (0.5 + random.random()))
    except TimeoutError:
        pass
    _SEARCH_DOWN["until"] = time.monotonic() + _SEARCH_COOLDOWN
    return []


async def fetch_page_text(url: str) -> str:
    """Open a URL and return readable text. Never raises."""

    if not re.match(r"^https?://", url.strip()):
        return "Ссылка должна начинаться с http:// или https://"
    try:
        async with httpx.AsyncClient(
            timeout=15, follow_redirects=True, headers={"User-Agent": _UA},
        ) as client:
            resp = await client.get(url)
            resp.raise_for_status()
            ctype = resp.headers.get("content-type", "")
            if "html" not in ctype and "text" not in ctype and ctype:
                return f"Не удалось прочитать содержимое ({ctype})."
            text = _strip_html(resp.text)
            return (text[:_FETCH_LIMIT]) or "Страница пустая."
    except Exception as exc:
        return f"Не удалось открыть ссылку ({exc.__class__.__name__})."
