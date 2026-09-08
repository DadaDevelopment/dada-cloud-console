"""SQL contracts for the retrieval tools, plus pure-Python oracles.

Each ``*_SQL`` string is the exact query a ``sql_query_v1`` manifest runs.
The matching ``search_*`` function reproduces the same ranking in memory so
tests and the offline eval can exercise the ranking without a database. If
the two ever disagree the test suite fails, which is the point: the ranking
is the product surface here, not an implementation detail.

Ranking is deliberately lexical (word-overlap), not embeddings. The corpus is
small, the queries are short technical nouns, and a wrong-but-confident
semantic neighbour is worse than no hit at all for an agent that must say
"не знаю" rather than improvise.
"""

import re

_WORD_RE = re.compile(r"[a-z0-9а-яё_.+-]{3,}")

NEWS_SEARCH_SQL = """
WITH q AS (
  SELECT DISTINCT w FROM unnest(regexp_split_to_array(lower(trim($1)), '[^a-z0-9а-яё_.+-]+')) AS w
  WHERE length(w) >= 3
)
SELECT n.source, n.title, n.url, n.summary,
       to_char(n.published_at, 'YYYY-MM-DD') AS published_on,
       (SELECT count(*) FROM q WHERE position(q.w in lower(n.title)) > 0) * 3
     + (SELECT count(*) FROM q WHERE position(q.w in lower(n.summary)) > 0)
     + (SELECT count(*) FROM q WHERE q.w = ANY(n.tags)) * 2 AS score
FROM ai_news n
WHERE (n.published_at IS NULL OR n.published_at > now() - make_interval(days => $2::int))
ORDER BY score DESC, n.published_at DESC NULLS LAST
LIMIT 5
"""

FACT_SEARCH_SQL = """
WITH q AS (
  SELECT DISTINCT w FROM unnest(regexp_split_to_array(lower(trim($1)), '[^a-z0-9а-яё_.+-]+')) AS w
  WHERE length(w) >= 3
)
SELECT f.id, f.subject, f.claim, f.source_url,
       to_char(f.checked_on, 'YYYY-MM-DD') AS checked_on,
       CASE WHEN lower(trim($1)) = lower(f.id) THEN 100000 ELSE 0 END
     + (SELECT count(*) FROM q WHERE position(q.w in lower(f.subject)) > 0) * 3
     + (SELECT count(*) FROM q WHERE q.w = ANY(f.keywords)) * 2
     + (SELECT count(*) FROM q WHERE position(q.w in lower(f.claim)) > 0) AS score
FROM tool_facts f
ORDER BY score DESC, f.id
LIMIT 3
"""

CHANNEL_SEARCH_SQL = """
WITH q AS (
  SELECT DISTINCT w FROM unnest(regexp_split_to_array(lower(trim($1)), '[^a-z0-9а-яё_.+-]+')) AS w
  WHERE length(w) >= 3
)
SELECT p.message_id, left(p.text, 600) AS text,
       to_char(p.posted_at, 'YYYY-MM-DD') AS posted_on,
       (SELECT count(*) FROM q WHERE position(q.w in lower(p.text)) > 0) AS score
FROM channel_posts p
ORDER BY score DESC, p.posted_at DESC NULLS LAST
LIMIT 3
"""

REPLY_BUDGET_SQL = """
SELECT count(*) AS answered_last_hour
FROM reply_ledger
WHERE chat_id = $1 AND decision = 'answered' AND created_at > now() - interval '1 hour'
"""


def _words(query: str) -> set[str]:
    return set(_WORD_RE.findall(query.lower()))


def search_news(items: list[dict], query: str, days: int) -> list[dict]:
    """Offline twin of NEWS_SEARCH_SQL. ``items`` carry an ``age_days`` int."""
    q = _words(query)
    scored = []
    for item in items:
        if item.get("age_days") is not None and item["age_days"] > days:
            continue
        title = item.get("title", "").lower()
        summary = item.get("summary", "").lower()
        tags = {t.lower() for t in item.get("tags", [])}
        score = (
            3 * sum(1 for w in q if w in title)
            + sum(1 for w in q if w in summary)
            + 2 * len(q & tags)
        )
        scored.append((score, item))
    scored.sort(key=lambda row: (-row[0], -(row[1].get("age_days") is None), row[1].get("age_days", 0)))
    return [item for score, item in scored[:5] if score > 0]


def search_facts(facts: list[dict], query: str) -> list[dict]:
    """Offline twin of FACT_SEARCH_SQL."""
    q = _words(query)
    scored = []
    for fact in facts:
        exact = 100000 if query.strip().lower() == fact["id"].lower() else 0
        subject = fact.get("subject", "").lower()
        claim = fact.get("claim", "").lower()
        keywords = {k.lower() for k in fact.get("keywords", [])}
        score = (
            exact
            + 3 * sum(1 for w in q if w in subject)
            + 2 * len(q & keywords)
            + sum(1 for w in q if w in claim)
        )
        scored.append((score, fact))
    scored.sort(key=lambda row: (-row[0], row[1]["id"]))
    return [fact for score, fact in scored[:3] if score > 0]
