"""The observation intake, kept free of the web framework on purpose.

``server.py`` cannot be imported without fastmcp/starlette/uvicorn installed,
and CI installs a bare ``python3`` from apk with no wheels at all. A test that
needed the framework could only be skipped there, and a skipped test is a test
that never runs: build #18 failed on ``No module named 'starlette'`` and the
route it was meant to guard was the one shipping that day.

So the decisions live here as plain functions over plain values -- what the
route answers, and who is allowed to reach it -- and ``server.py`` holds only
the adapter that turns a Request into those values. Everything worth asserting
is reachable from the standard library.
"""


def authorized(headers: dict, expected: str) -> bool:
    """Whether one request's headers carry the configured bearer token.

    ``headers`` keys are expected lowercased, as ASGI delivers them.
    """
    return headers.get("authorization") == expected


async def handle_observation(read_json, record):
    """Decide the answer for one posted observation.

    Returns ``(status, body)``. Never raises: the telegram gateway posts here
    inside its poll loop, and an exception on this path stops the bot from
    answering anyone, which is a far worse outcome than one lost observation.
    """
    try:
        payload = await read_json()
    except Exception:
        return 400, {"ok": False, "error": "invalid json"}
    if not isinstance(payload, dict) or not payload.get("message_id"):
        return 400, {"ok": False, "error": "no message_id"}
    try:
        await record(payload)
    except Exception as exc:
        return 500, {"ok": False, "error": str(exc)}
    return 200, {"ok": True}
