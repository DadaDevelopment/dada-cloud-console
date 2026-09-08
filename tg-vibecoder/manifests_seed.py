"""Default ``tool_manifests`` rows for the vibecoder channel agent.

Same Generic Tool Runtime contract as tg-agent-tools: a tool is a row, its
executable body is one of two primitives, and there is no per-tool Python.
Adding a source of truth here means adding a table and one SQL string, not a
new handler.
"""

from datetime import date

import facts
from search_sql import CHANNEL_SEARCH_SQL, FACT_SEARCH_SQL, NEWS_SEARCH_SQL

SEED_TAG = "vibecoder_v1_2026_09_08"

DEFAULT_MANIFESTS = [
    {
        "name": "news_search",
        "description": (
            "Свежие новости и релизы по ИИ, моделям, тулингу. Единственный источник для версий, цен, "
            "лимитов и «что вышло». Запрос словами по теме, days ограничивает окно. Пустой результат "
            "означает, что данных нет, а не что события не было."
        ),
        "op_type": "sql_query_v1",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "days": {"type": "integer", "default": 30},
            },
            "required": ["query", "days"],
        },
        "config": {"query": NEWS_SEARCH_SQL, "param_order": ["query", "days"]},
    },
    {
        "name": "fact_lookup",
        "description": (
            "Устойчивые технические утверждения: что такое штука, чем она кусается, какой размен. "
            "Точный id статьи работает лучше свободного запроса. checked_on это дата последней "
            "человеческой проверки, старая дата повод оговориться."
        ),
        "op_type": "sql_query_v1",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string"}},
            "required": ["query"],
        },
        "config": {"query": FACT_SEARCH_SQL, "param_order": ["query"]},
    },
    {
        "name": "channel_search",
        "description": (
            "Что канал уже писал по теме. Нужен, чтобы не противоречить собственным постам "
            "и не пересказывать их заново. Не источник внешних фактов."
        ),
        "op_type": "sql_query_v1",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string"}},
            "required": ["query"],
        },
        "config": {"query": CHANNEL_SEARCH_SQL, "param_order": ["query"]},
    },
]


async def seed(storage_module) -> None:
    """Plant facts and manifests.

    Facts are insert-only on conflict: once a row exists a human owns it, and
    this file must not silently overwrite an edited claim. Manifests are
    refreshed, but only the ones still carrying this seed tag.
    """
    fact_rows = [
        (
            f["id"],
            f["subject"],
            f["keywords"],
            f["claim"],
            f.get("source_url", ""),
            date.fromisoformat(f["checked_on"]) if f.get("checked_on") else None,
        )
        for f in facts.FACTS
    ]
    await storage_module.seed_rows(
        "tool_facts",
        ["id", "subject", "keywords", "claim", "source_url", "checked_on"],
        fact_rows,
        ["id"],
    )
    for manifest in DEFAULT_MANIFESTS:
        await storage_module.seed_manifest_if_missing(
            manifest["name"],
            manifest["description"],
            manifest["input_schema"],
            manifest["op_type"],
            manifest["config"],
            SEED_TAG,
        )
    await storage_module.refresh_manifests_from_seed(SEED_TAG, DEFAULT_MANIFESTS)
