"""MCP server for the vibecoder channel agent.

Same Generic Tool Runtime shape as tg-agent-tools: retrieval tools are
``tool_manifests`` rows executed by one of two primitives in ``ops.py``, and
this process holds only plumbing that every agent needs regardless of its
business content.

Static here, and deliberately not manifests:

``load_skill``
    Reads a domain file off disk. It is file IO, not a data query, and the
    files are versioned in git next to the persona.
``record_reply`` / ``reply_budget``
    The anti-spam ledger. A channel comment section punishes an agent that
    answers everything, so the budget is a first-class tool the persona is
    told to consult, not a hidden runtime filter. ``answered`` without reply
    text is refused: a first live run logged ``answered`` with an empty reply
    and returned the word "Ответил." to the caller, so the ledger claimed a
    reply that no human ever saw.
``today``
    The model has no clock. Without it, "свежее" silently means "whatever the
    training data called recent", which is the exact failure this agent must
    not have.

Transport notes, both learned live:

The HTTP app is stateless. A streamable-http session lives in the memory of
the pod that created it, and the platform runs this app with two replicas, so
a session opened on one pod and used on the other dies as "Session terminated"
roughly half the time.

The app requires a bearer token when ``MCP_AUTH_TOKEN`` is set. Every app here
gets a public domain by default, so this tool server answered ``/mcp`` from the
open internet with no credential at all; the kagent side passes the same token
as an ``Authorization`` header on the RemoteMCPServer.
"""

import asyncio
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

import uvicorn
from fastmcp import FastMCP
from fastmcp.tools.function_tool import FunctionTool
from starlette.middleware import Middleware

_AGENTKIT = os.environ.get("AGENTKIT_PATH") or str(Path(__file__).parent.parent / "agentkit")
if _AGENTKIT not in sys.path:
    sys.path.insert(0, _AGENTKIT)

import ledger
import manifests_seed
import ops
import skills
import storage

mcp = FastMCP("vibecoder")

REFRESH_INTERVAL_SECONDS = float(os.environ.get("MANIFEST_REFRESH_SECONDS", "10"))
AGENT_NAME = os.environ.get("AGENT_NAME", "vibecoder")
AGENT_ROOT = Path(os.environ.get("AGENT_ROOT", Path(__file__).parent / "agents"))
HOURLY_REPLY_BUDGET = int(os.environ.get("HOURLY_REPLY_BUDGET", "8"))

IDENTIFIER_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")

SKILLS = skills.SkillSet(str(AGENT_ROOT.parent), AGENT_NAME)

_registered: dict[str, dict] = {}


def _dynamic_fn(param_names: list[str], required: set[str], handler):
    for p in param_names:
        if not IDENTIFIER_RE.match(p):
            raise ValueError(f"manifest property is not a valid identifier: {p!r}")
    ordered = [p for p in param_names if p in required] + [p for p in param_names if p not in required]
    args = [p if p in required else f"{p}=None" for p in ordered]
    src = f"async def _manifest_tool({', '.join(args)}):\n    return await handler(**locals())\n"
    namespace = {"handler": handler}
    exec(src, namespace)
    return namespace["_manifest_tool"]


def register_manifest(manifest: dict) -> None:
    op_type = manifest["op_type"]
    if op_type not in ops.OPS:
        raise ValueError(f"unknown op_type in manifest {manifest['name']!r}: {op_type!r}")
    op_fn = ops.OPS[op_type]
    config = manifest["config"]

    async def handler(**kwargs):
        return await op_fn(config, **kwargs)

    schema = manifest["input_schema"]
    fn = _dynamic_fn(list(schema.get("properties", {}).keys()), set(schema.get("required", [])), handler)
    tool = FunctionTool.from_function(fn, name=manifest["name"], description=manifest["description"])
    tool.parameters = schema
    mcp.add_tool(tool)
    mcp.enable(names={manifest["name"]}, components={"tool"})


async def sync_manifests() -> None:
    """One poll tick: reconcile registered MCP tools against tool_manifests rows."""
    current = {m["name"]: m for m in await storage.list_manifests()}
    for name, manifest in current.items():
        if _registered.get(name) != manifest:
            try:
                register_manifest(manifest)
            except Exception:
                continue
            _registered[name] = manifest
    for name in list(_registered):
        if name not in current:
            mcp.disable(names={name}, components={"tool"})
            del _registered[name]


async def refresh_loop() -> None:
    """Runs for the life of the process; isolates poll-tick failures from each other."""
    while True:
        try:
            await sync_manifests()
        except Exception:
            pass
        await asyncio.sleep(REFRESH_INTERVAL_SECONDS)


@mcp.tool
async def load_skill(domain: str) -> dict:
    """Загрузить процедуру для текущей задачи: takes, debug, news, banter, boundaries."""
    try:
        return {"found": True, "domain": domain, "instruction": SKILLS.load(domain)}
    except skills.SkillError as exc:
        return {"found": False, "error": str(exc), "available": SKILLS.domains()}


@mcp.tool
async def reply_budget(chat_id: str) -> dict:
    """Сколько ответов уже отправлено в этот чат за последний час и сколько осталось по бюджету."""
    used = await storage.replies_last_hour(chat_id)
    return {"used_last_hour": used, "limit": HOURLY_REPLY_BUDGET, "remaining": max(0, HOURLY_REPLY_BUDGET - used)}


@mcp.tool
async def record_reply(
    chat_id: str,
    decision: str,
    thread_id: str = "",
    message_id: str = "",
    author: str = "",
    incoming: str = "",
    reply: str = "",
) -> dict:
    """Записать решение по комментарию. decision: answered или skipped. skipped не тратит бюджет."""
    problem = ledger.validate_decision(decision, reply)
    if problem:
        return {"ok": False, "error": problem}
    row_id = await storage.log_reply(chat_id, thread_id, message_id, author, incoming, reply, decision)
    return {"ok": True, "id": row_id}


@mcp.tool
async def today() -> dict:
    """Текущая дата в UTC. Всё, что старше её на месяцы, считается неподтверждённым."""
    now = datetime.now(timezone.utc)
    return {"date": now.date().isoformat(), "iso": now.isoformat(), "weekday": now.strftime("%A")}


async def bootstrap() -> None:
    await storage.pg_pool()
    await manifests_seed.seed(storage)
    await sync_manifests()


MCP_AUTH_TOKEN = os.environ.get("MCP_AUTH_TOKEN", "")


class BearerGate:
    """Reject unauthenticated calls when a token is configured.

    Written as raw ASGI rather than as a fastmcp auth provider because the
    check has to sit in front of the transport: an unauthenticated caller must
    not get as far as opening a session.
    """

    def __init__(self, app, token: str):
        self.app = app
        self.expected = f"Bearer {token}"

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        headers = {k.decode().lower(): v.decode() for k, v in scope.get("headers", [])}
        if headers.get("authorization") != self.expected:
            await send({"type": "http.response.start", "status": 401,
                        "headers": [(b"content-type", b"text/plain; charset=utf-8")]})
            await send({"type": "http.response.body", "body": b"unauthorized"})
            return
        await self.app(scope, receive, send)


async def main() -> None:
    await bootstrap()
    asyncio.create_task(refresh_loop())
    middleware = [Middleware(BearerGate, token=MCP_AUTH_TOKEN)] if MCP_AUTH_TOKEN else []
    app = mcp.http_app(path="/mcp", stateless_http=True, middleware=middleware)
    config = uvicorn.Config(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8000")), log_config=None)
    await uvicorn.Server(config).serve()


if __name__ == "__main__":
    asyncio.run(main())
