"""Langfuse v4 trace shaping for one A2A turn on a Dada agent.

kagent traces a turn as ``POST /`` -> ``invocation`` -> ``invoke_agent`` -> ...
with a random ``user_id``/``session_id`` derived from the A2A contextId and a
root span that carries nothing Langfuse can show. Callers (agent-runtime,
tg-gateway) put the sender's identity into the A2A ``message.metadata`` under
``dada.*`` keys; this module turns that into the ``langfuse.*`` attributes the
v4 ingestion reads:

* trace-level attributes (user, session, name, tags, metadata, release,
  environment, prompt link) go into the kagent attribute context, so
  ``KagentAttributesSpanProcessor`` stamps them on every child span, and are
  also set directly on the root span, which started before the context did;
* the root span gets a readable name, ``langfuse.observation.type=agent``,
  the user text as input, the final answer as output, and level ERROR on a
  failed task.

``begin_turn`` runs before the ADK runner starts, ``end_turn``/``fail_turn``
when the executor publishes the final task event. The turn state lives in a
contextvar so the executor stays free of plumbing.

Prompt versions are push-only and ride the same delivery as the prompt itself:
git (argo-infra ManagedAgent) -> ConfigMap -> this pod. ``register_prompt`` is
called at boot with the system prompt the pod was started with and mirrors it
into the agent's Langfuse project as a text prompt named after the agent
(label ``production``, commit message = ``PROMPT_VERSION``). Langfuse versions
are append-only, so identical text is never re-published; the resulting integer
version is what every span links to through ``langfuse.observation.prompt.*``.
No console call, no CI step outside the rollout, no read-back into git.
"""

from __future__ import annotations

import base64
import contextvars
import json
import logging
import os
import threading
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Optional

from opentelemetry import trace

logger = logging.getLogger(__name__)

META_PREFIX = "dada."


@dataclass
class DadaTurn:
    root: trace.Span
    agent: str
    channel: str
    trigger: str
    attributes: dict[str, Any] = field(default_factory=dict)


_current_turn: contextvars.ContextVar[Optional[DadaTurn]] = contextvars.ContextVar("dada_turn", default=None)


def _metadata(context: Any) -> dict[str, str]:
    message = getattr(context, "message", None)
    raw = getattr(message, "metadata", None) or {}
    out: dict[str, str] = {}
    for key, value in raw.items():
        if isinstance(key, str) and key.startswith(META_PREFIX) and value not in (None, ""):
            out[key[len(META_PREFIX) :]] = str(value)
    return out


def _message_text(message: Any) -> str:
    texts: list[str] = []
    for part in getattr(message, "parts", None) or []:
        root = getattr(part, "root", part)
        text = getattr(root, "text", None)
        if isinstance(text, str) and text:
            texts.append(text)
    return "\n".join(texts)


def _user_and_session(meta: dict[str, str], fallback_user: str, fallback_session: str) -> tuple[str, str]:
    channel = meta.get("channel", "")
    username = meta.get("username", "")
    chat_id = meta.get("chat_id", "")
    user_id = meta.get("user_id") or chat_id
    if username:
        user = "@" + username.lstrip("@")
    elif channel and user_id:
        user = f"{channel}:{user_id}"
    else:
        return fallback_user, fallback_session
    session = user
    if chat_id and chat_id != user_id:
        session = f"{user}@{chat_id}"
    if meta.get("thread_id"):
        session = f"{session}#{meta['thread_id']}"
    return user, session


LANGFUSE_DEFAULT_HOST = "https://cloud.langfuse.com"
LANGFUSE_PROMPT_LABEL = "production"
PROMPT_SYNC_WAIT_SECONDS = 5.0

_prompt_link_state: dict[str, Any] = {}
_prompt_synced = threading.Event()
_prompt_synced.set()


def _langfuse_credentials() -> Optional[tuple[str, str, str]]:
    """(host, public key, secret key) from LANGFUSE_* env, else from the OTLP basic-auth header."""
    host = (os.getenv("LANGFUSE_HOST") or os.getenv("LANGFUSE_BASE_URL") or LANGFUSE_DEFAULT_HOST).rstrip("/")
    public = os.getenv("LANGFUSE_PUBLIC_KEY", "").strip()
    secret = os.getenv("LANGFUSE_SECRET_KEY", "").strip()
    if public and secret:
        return host, public, secret
    raw = os.getenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS") or os.getenv("OTEL_EXPORTER_OTLP_HEADERS") or ""
    for pair in raw.split(","):
        key, _, value = pair.strip().partition("=")
        if key.strip().lower() != "authorization":
            continue
        scheme, _, token = urllib.parse.unquote(value.strip()).partition(" ")
        if scheme.lower() != "basic":
            continue
        try:
            decoded = base64.b64decode(token.strip()).decode()
        except (ValueError, UnicodeDecodeError):
            continue
        public, _, secret = decoded.partition(":")
        if public and secret:
            return host, public, secret
    return None


def _langfuse_json(creds: tuple[str, str, str], method: str, path: str, body: Optional[dict[str, Any]] = None) -> Any:
    host, public, secret = creds
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(host + path, data=data, method=method)
    request.add_header("Authorization", "Basic " + base64.b64encode(f"{public}:{secret}".encode()).decode())
    request.add_header("Accept", "application/json")
    if data is not None:
        request.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(request, timeout=15) as response:
        return json.load(response)


def ensure_prompt_version(creds: tuple[str, str, str], name: str, text: str, commit_message: str) -> int:
    """Version of the Langfuse text prompt ``name`` whose text equals ``text``, publishing one only when production differs."""
    path = "/api/public/v2/prompts/" + urllib.parse.quote(name, safe="") + "?label=" + LANGFUSE_PROMPT_LABEL
    try:
        current = _langfuse_json(creds, "GET", path)
        if current.get("type") == "text" and current.get("prompt") == text:
            return int(current["version"])
    except urllib.error.HTTPError as exc:
        if exc.code != 404:
            raise
    body: dict[str, Any] = {"name": name, "type": "text", "prompt": text, "labels": [LANGFUSE_PROMPT_LABEL]}
    if commit_message:
        body["commitMessage"] = commit_message
    created = _langfuse_json(creds, "POST", "/api/public/v2/prompts", body)
    return int(created["version"])


def _sync_prompt(name: str, text: str, version_label: str) -> None:
    try:
        creds = _langfuse_credentials()
        if creds is None:
            logger.info("dada tracing: no Langfuse credentials, prompt %s runs without a prompt link", name)
            return
        version = ensure_prompt_version(creds, name, text, version_label)
        _prompt_link_state.update({
            "langfuse.observation.prompt.name": name,
            "langfuse.observation.prompt.version": version,
        })
        logger.info("dada tracing: prompt %s %s is Langfuse version %d", name, version_label, version)
    except Exception:
        logger.warning("dada tracing: prompt %s: Langfuse sync failed, running without a prompt link", name, exc_info=True)
    finally:
        _prompt_synced.set()


def register_prompt(text: Optional[str]) -> None:
    """Mirror the system prompt this pod boots with into Langfuse, off the request path."""
    name = os.getenv("KAGENT_NAME", "")
    text = (text or "").strip()
    if not name or not text:
        return
    _prompt_synced.clear()
    threading.Thread(
        target=_sync_prompt, args=(name, text, os.getenv("PROMPT_VERSION", "")), name="dada-langfuse-prompt", daemon=True
    ).start()


def _prompt_link() -> dict[str, Any]:
    _prompt_synced.wait(PROMPT_SYNC_WAIT_SECONDS)
    return dict(_prompt_link_state)


def build_attributes(agent: str, meta: dict[str, str], fallback_user: str, fallback_session: str) -> dict[str, Any]:
    """Trace-level Langfuse attributes for the turn, safe to stamp on every span."""
    channel = meta.get("channel", "a2a")
    trigger = meta.get("trigger", "turn")
    user, session = _user_and_session(meta, fallback_user, fallback_session)
    attrs: dict[str, Any] = {
        "langfuse.user.id": user,
        "langfuse.session.id": session,
        "langfuse.trace.name": f"{agent} {channel} {trigger}",
        "langfuse.trace.tags": [channel, trigger, agent],
        "langfuse.environment": os.getenv("LANGFUSE_TRACING_ENVIRONMENT", "default"),
    }
    if os.getenv("PROMPT_VERSION"):
        attrs["langfuse.release"] = os.environ["PROMPT_VERSION"]
        attrs["langfuse.trace.metadata.prompt_version"] = os.environ["PROMPT_VERSION"]
    for key, value in meta.items():
        attrs[f"langfuse.trace.metadata.{key}"] = value
    attrs.update(_prompt_link())
    return attrs


def begin_turn(context: Any, run_args: dict[str, Any], span_attributes: dict[str, Any]) -> Optional[DadaTurn]:
    """Shape the root span and extend ``span_attributes`` with Langfuse trace attributes."""
    try:
        meta = _metadata(context)
        agent = os.getenv("KAGENT_NAME") or run_args.get("app_name") or "agent"
        attrs = build_attributes(
            agent, meta, str(run_args.get("user_id") or ""), str(run_args.get("session_id") or "")
        )
        span_attributes.update(attrs)
        root = trace.get_current_span()
        turn = DadaTurn(root=root, agent=agent, channel=meta.get("channel", "a2a"), trigger=meta.get("trigger", "turn"), attributes=attrs)
        if root.is_recording():
            root.update_name(attrs["langfuse.trace.name"])
            root.set_attributes(attrs)
            root.set_attribute("langfuse.observation.type", "agent")
            root.set_attribute("langfuse.observation.input", _message_text(getattr(context, "message", None)) or "(no text)")
        _current_turn.set(turn)
        return turn
    except Exception:
        logger.warning("dada tracing: begin_turn failed", exc_info=True)
        return None


def end_turn(task_state: Any, status_message: Any, run_metadata: Optional[dict[str, Any]] = None) -> None:
    """Record the final answer (or the non-completed state) on the root span."""
    turn = _current_turn.get()
    if turn is None:
        return
    try:
        root = turn.root
        if not root.is_recording():
            return
        state = getattr(task_state, "value", task_state)
        output = _message_text(status_message)
        root.set_attribute("langfuse.observation.output", output or f"(task {state}, no text)")
        root.set_attribute("langfuse.observation.metadata.task_state", str(state))
        if state not in ("working", "completed"):
            root.set_attribute("langfuse.observation.level", "WARNING")
            root.set_attribute("langfuse.observation.status_message", f"task {state}")
        usage = (run_metadata or {}).get("kagent_usage_metadata") or {}
        if isinstance(usage, dict):
            for src, dst in (("prompt_token_count", "input"), ("candidates_token_count", "output"), ("total_token_count", "total")):
                if isinstance(usage.get(src), int):
                    root.set_attribute(f"langfuse.observation.usage_details.{dst}", usage[src])
    except Exception:
        logger.warning("dada tracing: end_turn failed", exc_info=True)


def fail_turn(error_message: str) -> None:
    """Mark the root span as failed with the error text as output."""
    turn = _current_turn.get()
    if turn is None:
        return
    try:
        root = turn.root
        if not root.is_recording():
            return
        root.set_attribute("langfuse.observation.output", error_message or "(failed, no message)")
        root.set_attribute("langfuse.observation.level", "ERROR")
        root.set_attribute("langfuse.observation.status_message", (error_message or "failed")[:500])
    except Exception:
        logger.warning("dada tracing: fail_turn failed", exc_info=True)


def exporter_headers(endpoint: str) -> Optional[dict[str, str]]:
    """OTLP headers from env plus the Langfuse v4 ingestion marker when the sink is Langfuse."""
    version = os.getenv("LANGFUSE_INGESTION_VERSION", "4" if "langfuse" in (endpoint or "") else "")
    if not version:
        return None
    from opentelemetry.util.re import parse_env_headers

    raw = os.getenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS") or os.getenv("OTEL_EXPORTER_OTLP_HEADERS") or ""
    headers = dict(parse_env_headers(raw, liberal=True)) if raw else {}
    headers["x-langfuse-ingestion-version"] = version
    return headers
