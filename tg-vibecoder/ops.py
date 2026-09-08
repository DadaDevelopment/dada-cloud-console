"""Generic op_type dispatchers for manifest-driven tools.

Exactly two op_types exist: http_call_v1 (call an HTTP endpoint) and
sql_query_v1 (run a parameterized SQL query). A tool_manifests row picks
one op_type and supplies config — no per-vendor, per-client Python code.
Business logic (geo/jurisdiction rules, KB answers, KYC checklists,
operator round-robin) lives as SQL + seeded rows, not as named functions
here.
"""

import re

import storage

_PLACEHOLDER_RE = re.compile(r"\{([A-Za-z_][A-Za-z0-9_]*)\}")


def _fill_template(node, args: dict):
    if isinstance(node, str):
        if node.startswith("{") and node.endswith("}") and node.count("{") == 1 and node[1:-1] in args:
            return args[node[1:-1]]
        return _PLACEHOLDER_RE.sub(lambda m: str(args.get(m.group(1), m.group(0))), node)
    if isinstance(node, dict):
        return {k: _fill_template(v, args) for k, v in node.items()}
    if isinstance(node, list):
        return [_fill_template(v, args) for v in node]
    return node


async def http_call_v1(config: dict, **args) -> dict:
    """Call an HTTP endpoint.

    ``pass_error_response`` is opt-in and only for trusted control APIs whose
    structured rejection lets the caller repair a malformed request. Other
    integrations fail closed.
    """
    import httpx

    method = config.get("method", "POST")
    url = _fill_template(config["url"], args)
    headers = _fill_template(config.get("headers", {}), args)
    body = _fill_template(config.get("body"), args)
    async with httpx.AsyncClient(timeout=15.0) as client:
        resp = await client.request(method, url, headers=headers, json=body)
        if not config.get("pass_error_response", False):
            resp.raise_for_status()
        return {"status_code": resp.status_code, "body": resp.json() if resp.content else None}


async def sql_query_v1(config: dict, **args) -> list[dict]:
    """Run a parameterized SQL query declared by a manifest row."""
    query = config["query"]
    params = [args[p] for p in config.get("param_order", [])]
    pool = await storage.pg_pool()
    async with pool.acquire() as conn:
        rows = await conn.fetch(query, *params)
    return [dict(row) for row in rows]


OPS = {
    "http_call_v1": http_call_v1,
    "sql_query_v1": sql_query_v1,
}
