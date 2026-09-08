"""Postgres access for the vibecoder channel agent.

Owns five durable tables:

``tool_manifests``
    Generic Tool Runtime rows, same contract as tg-agent-tools: one row per
    tool, ``op_type`` is one of the two primitives in ``ops.py``.
``ai_news``
    Freshness corpus. One row per ingested item (release notes, changelogs,
    posts). ``fingerprint`` is the dedupe key so a re-run of the ingest cron
    is idempotent.
``tool_facts``
    Curated, hand-maintained claims about tooling (what a thing is, current
    status, price shape, known sharp edge). Separate from ``ai_news`` because
    a fact is asserted and dated by a human, a news row is only observed.
``channel_posts``
    The channel's own published posts, so the agent can speak in the line the
    channel already took instead of contradicting it in its own comments.
``reply_ledger``
    One row per reply decision. Backs the per-chat hourly budget and makes
    "did the agent actually answer" auditable without reading Telegram.
``chat_comments``
    Everything said in the discussion chat, whether or not the agent replied.
    The agent is handed only the messages it is answering, so without this it
    knows the questions and nothing about the room they were asked in: which
    words the regulars use, what was already argued out, who is who.
"""

import json
import os
import sys
from datetime import datetime
from pathlib import Path

import asyncpg

_AGENTKIT = os.environ.get("AGENTKIT_PATH") or str(Path(__file__).parent.parent / "agentkit")
if _AGENTKIT not in sys.path:
    sys.path.insert(0, _AGENTKIT)

import transcript

_pg_pool: asyncpg.Pool | None = None

SCHEMA = [
    """
    CREATE TABLE IF NOT EXISTS tool_manifests (
        id BIGSERIAL PRIMARY KEY,
        name TEXT NOT NULL UNIQUE,
        description TEXT NOT NULL,
        input_schema JSONB NOT NULL,
        op_type TEXT NOT NULL,
        config JSONB NOT NULL DEFAULT '{}'::jsonb,
        enabled BOOLEAN NOT NULL DEFAULT true,
        seeded_from TEXT NOT NULL DEFAULT '',
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS ai_news (
        id BIGSERIAL PRIMARY KEY,
        fingerprint TEXT NOT NULL UNIQUE,
        source TEXT NOT NULL,
        title TEXT NOT NULL,
        url TEXT NOT NULL,
        summary TEXT NOT NULL DEFAULT '',
        tags TEXT[] NOT NULL DEFAULT '{}',
        published_at TIMESTAMPTZ,
        fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )
    """,
    "CREATE INDEX IF NOT EXISTS ai_news_published_idx ON ai_news (published_at DESC NULLS LAST)",
    """
    CREATE TABLE IF NOT EXISTS tool_facts (
        id TEXT PRIMARY KEY,
        subject TEXT NOT NULL,
        keywords TEXT[] NOT NULL DEFAULT '{}',
        claim TEXT NOT NULL,
        source_url TEXT NOT NULL DEFAULT '',
        checked_on DATE
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS channel_posts (
        message_id BIGINT PRIMARY KEY,
        text TEXT NOT NULL,
        posted_at TIMESTAMPTZ,
        ingested_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS reply_ledger (
        id BIGSERIAL PRIMARY KEY,
        chat_id TEXT NOT NULL,
        thread_id TEXT NOT NULL DEFAULT '',
        message_id TEXT NOT NULL DEFAULT '',
        author TEXT NOT NULL DEFAULT '',
        incoming TEXT NOT NULL DEFAULT '',
        reply TEXT NOT NULL DEFAULT '',
        decision TEXT NOT NULL DEFAULT 'answered',
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )
    """,
    "CREATE INDEX IF NOT EXISTS reply_ledger_chat_time_idx ON reply_ledger (chat_id, created_at DESC)",
    """
    CREATE TABLE IF NOT EXISTS chat_comments (
        chat_id BIGINT NOT NULL,
        message_id BIGINT NOT NULL,
        thread_id BIGINT NOT NULL DEFAULT 0,
        author TEXT NOT NULL DEFAULT '',
        username TEXT NOT NULL DEFAULT '',
        text TEXT NOT NULL DEFAULT '',
        is_channel_post BOOLEAN NOT NULL DEFAULT false,
        reply_to_message_id BIGINT NOT NULL DEFAULT 0,
        engaged BOOLEAN NOT NULL DEFAULT false,
        reason TEXT NOT NULL DEFAULT '',
        sent_at TIMESTAMPTZ,
        observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        PRIMARY KEY (chat_id, message_id)
    )
    """,
    "ALTER TABLE chat_comments ADD COLUMN IF NOT EXISTS media TEXT NOT NULL DEFAULT ''",
    "CREATE INDEX IF NOT EXISTS chat_comments_time_idx ON chat_comments (sent_at DESC NULLS LAST)",
    "CREATE INDEX IF NOT EXISTS chat_comments_thread_idx ON chat_comments (chat_id, thread_id)",
]


async def pg_pool() -> asyncpg.Pool:
    """Return the shared pool, creating the schema on first acquisition."""
    global _pg_pool
    if _pg_pool is None:
        _pg_pool = await asyncpg.create_pool(os.environ["DATABASE_URL"], min_size=1, max_size=5)
        async with _pg_pool.acquire() as conn:
            for stmt in SCHEMA:
                await conn.execute(stmt)
    return _pg_pool


async def list_manifests() -> list[dict]:
    pool = await pg_pool()
    async with pool.acquire() as conn:
        rows = await conn.fetch(
            "SELECT name, description, input_schema, op_type, config FROM tool_manifests WHERE enabled ORDER BY name"
        )
    return [
        {
            "name": row["name"],
            "description": row["description"],
            "input_schema": json.loads(row["input_schema"]),
            "op_type": row["op_type"],
            "config": json.loads(row["config"]),
        }
        for row in rows
    ]


async def seed_manifest_if_missing(
    name: str, description: str, input_schema: dict, op_type: str, config: dict, seeded_from: str
) -> None:
    pool = await pg_pool()
    async with pool.acquire() as conn:
        await conn.execute(
            """
            INSERT INTO tool_manifests (name, description, input_schema, op_type, config, seeded_from)
            VALUES ($1, $2, $3::jsonb, $4, $5::jsonb, $6)
            ON CONFLICT (name) DO NOTHING
            """,
            name,
            description,
            json.dumps(input_schema),
            op_type,
            json.dumps(config),
            seeded_from,
        )


async def refresh_manifests_from_seed(seed_tag: str, manifests: list[dict]) -> None:
    """Overwrite rows this seed tag owns and disable ones it dropped.

    A row a human edited (its ``seeded_from`` no longer matches) is left
    alone: the console is the intended editor of a manifest, this file only
    owns what it planted.
    """
    pool = await pg_pool()
    names = [m["name"] for m in manifests]
    async with pool.acquire() as conn:
        async with conn.transaction():
            for m in manifests:
                await conn.execute(
                    """
                    UPDATE tool_manifests
                    SET description = $2, input_schema = $3::jsonb, op_type = $4, config = $5::jsonb,
                        enabled = true, updated_at = now()
                    WHERE name = $1 AND seeded_from = $6
                    """,
                    m["name"],
                    m["description"],
                    json.dumps(m["input_schema"]),
                    m["op_type"],
                    json.dumps(m["config"]),
                    seed_tag,
                )
            await conn.execute(
                "UPDATE tool_manifests SET enabled = false, updated_at = now() "
                "WHERE seeded_from = $1 AND NOT (name = ANY($2::text[]))",
                seed_tag,
                names,
            )


async def seed_rows(table: str, columns: list[str], rows: list[tuple], conflict_cols: list[str]) -> None:
    if not rows:
        return
    pool = await pg_pool()
    placeholders = ", ".join(f"${i + 1}" for i in range(len(columns)))
    sql = (
        f"INSERT INTO {table} ({', '.join(columns)}) VALUES ({placeholders}) "
        f"ON CONFLICT ({', '.join(conflict_cols)}) DO NOTHING"
    )
    async with pool.acquire() as conn:
        async with conn.transaction():
            for row in rows:
                await conn.execute(sql, *row)


async def upsert_news(items: list[dict]) -> int:
    """Insert ingested items, skipping fingerprints already stored.

    Returns the number of rows actually written, which is the only honest
    signal that an ingest run did anything.
    """
    if not items:
        return 0
    pool = await pg_pool()
    written = 0
    async with pool.acquire() as conn:
        async with conn.transaction():
            for item in items:
                status = await conn.execute(
                    """
                    INSERT INTO ai_news (fingerprint, source, title, url, summary, tags, published_at)
                    VALUES ($1, $2, $3, $4, $5, $6::text[], $7)
                    ON CONFLICT (fingerprint) DO NOTHING
                    """,
                    item["fingerprint"],
                    item["source"],
                    item["title"],
                    item["url"],
                    item.get("summary", ""),
                    item.get("tags", []),
                    item.get("published_at"),
                )
                if status.endswith(" 1"):
                    written += 1
    return written


async def upsert_channel_posts(posts: list[dict]) -> int:
    if not posts:
        return 0
    pool = await pg_pool()
    written = 0
    async with pool.acquire() as conn:
        async with conn.transaction():
            for post in posts:
                status = await conn.execute(
                    """
                    INSERT INTO channel_posts (message_id, text, posted_at)
                    VALUES ($1, $2, $3)
                    ON CONFLICT (message_id) DO UPDATE SET text = EXCLUDED.text
                    """,
                    post["message_id"],
                    post["text"],
                    post.get("posted_at"),
                )
                if status.endswith(" 1"):
                    written += 1
    return written


async def log_reply(
    chat_id: str, thread_id: str, message_id: str, author: str, incoming: str, reply: str, decision: str
) -> int:
    pool = await pg_pool()
    async with pool.acquire() as conn:
        return await conn.fetchval(
            """
            INSERT INTO reply_ledger (chat_id, thread_id, message_id, author, incoming, reply, decision)
            VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id
            """,
            chat_id,
            thread_id,
            message_id,
            author,
            incoming,
            reply,
            decision,
        )


async def replies_last_hour(chat_id: str) -> int:
    pool = await pg_pool()
    async with pool.acquire() as conn:
        return await conn.fetchval(
            "SELECT count(*) FROM reply_ledger WHERE chat_id = $1 AND decision = 'answered' "
            "AND created_at > now() - interval '1 hour'",
            chat_id,
        )


def _parse_ts(value):
    """Accept the ISO string the transport sends, or a datetime, or nothing."""
    if not value:
        return None
    if isinstance(value, datetime):
        return value
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None


async def record_comment(observation: dict) -> str:
    """Store one observed chat message, keyed by (chat, message).

    Upsert rather than insert: the gateway may see the same update twice after
    a restart, and a duplicated comment would quietly reweigh every ranking
    built on this table.

    ``media`` holds the same bracketed line the agent reads in a live turn, so
    a comment that was only a screenshot stops entering the corpus as a blank
    row nobody can search.
    """
    pool = await pg_pool()
    async with pool.acquire() as conn:
        return await conn.execute(
            """
            INSERT INTO chat_comments (
                chat_id, message_id, thread_id, author, username, text,
                is_channel_post, reply_to_message_id, engaged, reason, sent_at, media
            )
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
            ON CONFLICT (chat_id, message_id) DO UPDATE SET
                text = EXCLUDED.text,
                engaged = EXCLUDED.engaged,
                reason = EXCLUDED.reason,
                media = EXCLUDED.media
            """,
            int(observation.get("chat_id") or 0),
            int(observation.get("message_id") or 0),
            int(observation.get("thread_id") or 0),
            observation.get("first_name") or "",
            observation.get("username") or "",
            observation.get("text") or "",
            bool(observation.get("is_channel_post")),
            int(observation.get("reply_to_message_id") or 0),
            bool(observation.get("engaged")),
            observation.get("reason") or "",
            _parse_ts(observation.get("sent_at")),
            transcript.media_context(
                observation.get("media_kind") or "",
                observation.get("media_description") or "",
                observation.get("media_transcript") or "",
                observation.get("media_file_name") or "",
            ),
        )


async def comment_count(chat_id: int) -> int:
    pool = await pg_pool()
    async with pool.acquire() as conn:
        return await conn.fetchval("SELECT count(*) FROM chat_comments WHERE chat_id = $1", chat_id)
