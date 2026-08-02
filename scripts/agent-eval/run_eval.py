#!/usr/bin/env python3
"""Drive the dataset against a live console agent and record the transcripts.

One case is one conversation: clear the agent context for the scope, POST
``/agent/chat``, read the SSE stream, and answer any confirm card via
``/agent/chat/confirm`` until the turn reports ``done``.

Confirm cards are REJECTED by default. Approving them creates real, billable
resources on whatever environment DADA_API points at, so approval needs both
``--allow-writes`` and an explicit ``confirm_policy: approve`` on the case.

    DADA_BEARER=... DADA_PROJECT_ID=... DADA_ENV_ID=... \\
        python3 scripts/agent-eval/run_eval.py --repeats 3
"""

import argparse
import json
import os
import socket
import subprocess
import sys
import threading
import time
import urllib.error
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from hashlib import sha256
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import (
    api_base,
    bearer,
    eprint,
    http_json,
    http_sse,
    iter_sse,
    load_jsonl,
    write_jsonl,
)

MAX_CONFIRM_ROUNDS = 3
RETRY_BACKOFF_SEC = (2, 4, 8)


class DailyCapReached(Exception):
    """Raised when the backend refuses further messages for the day."""


def utc_stamp() -> str:
    """Compact UTC timestamp used to name a run directory."""
    return datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")


def git_sha(root: Path) -> str:
    """Current HEAD sha, or an empty string outside a checkout."""
    try:
        out = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=str(root),
            capture_output=True,
            text=True,
            timeout=10,
        )
        return out.stdout.strip() if out.returncode == 0 else ""
    except (OSError, subprocess.SubprocessError):
        return ""


def load_scopes() -> list:
    """Project/env scopes to spread cases over.

    A scope is a conversation: the agent keeps one history per (user, project,
    env), so two cases sharing a scope concurrently would poison each other.
    """
    raw = os.environ.get("DADA_EVAL_SCOPES", "").strip()
    if raw:
        parsed = json.loads(raw)
        return [{"projectId": s.get("projectId", ""), "envId": s.get("envId", "")} for s in parsed]
    return [
        {
            "projectId": os.environ.get("DADA_PROJECT_ID", ""),
            "envId": os.environ.get("DADA_ENV_ID", ""),
        }
    ]


class Collector:
    """Accumulates one case's SSE stream across the chat and confirm turns."""

    def __init__(self):
        self.text_parts = []
        self.tools_called = []
        self.confirm_cards = []
        self.events = []
        self.error = None
        self.trace = None
        self.awaiting_confirm = False

    @property
    def final_text(self) -> str:
        return "".join(self.text_parts)

    def feed(self, event, started_at):
        """Consume one SSE event; returns a pending card payload or None."""
        self.events.append({"event": event.event, "t_ms": int((time.monotonic() - started_at) * 1000)})
        if event.event == "token":
            self.text_parts.append(event.data)
            return None
        if event.event == "tool_call":
            try:
                name = json.loads(event.data).get("name")
            except ValueError:
                name = None
            if name:
                self.tools_called.append(name)
            return None
        if event.event == "trace":
            try:
                self.trace = json.loads(event.data)
            except ValueError:
                self.trace = {"raw": event.data}
            return None
        if event.event == "confirm_request":
            try:
                card = json.loads(event.data)
            except ValueError:
                return None
            if not card.get("action_id") or not card.get("tool_name"):
                return None
            return card
        if event.event == "error":
            try:
                payload = json.loads(event.data)
            except ValueError:
                payload = {"code": "upstream", "message": event.data}
            self.error = payload
            if payload.get("code") == "daily_cap":
                raise DailyCapReached(payload.get("message", "daily cap reached"))
            return None
        if event.event == "done":
            try:
                self.awaiting_confirm = bool(json.loads(event.data).get("awaiting_confirm"))
            except ValueError:
                self.awaiting_confirm = False
            return None
        return None


def decide(case, allow_writes: bool) -> str:
    """Approve a confirm card only when the case asks for it and writes are on."""
    if allow_writes and case.get("confirm_policy") == "approve":
        return "approve"
    return "reject"


def stream_turn(url, token, body, collector, started_at, timeout):
    """Read one SSE turn to completion, returning the pending card if any."""
    pending = None
    resp = http_sse(url, token, body, timeout=timeout)
    try:
        for event in iter_sse(resp):
            card = collector.feed(event, started_at)
            if card is not None:
                pending = card
            if event.event == "done":
                break
    finally:
        resp.close()
    return pending


def run_case(case, scope, repeat, args, token, base):
    """Run one case end to end and return its result record."""
    started_at = time.monotonic()
    collector = Collector()

    context = dict(case.get("input_context") or {})
    project_id = context.pop("projectId", scope["projectId"])
    env_id = context.pop("envId", scope["envId"])
    app_name = context.pop("appName", "")

    if not args.no_clear:
        http_json(
            "POST",
            "%s/agent/chat/context/clear" % base,
            token,
            {"projectId": project_id, "envId": env_id},
            timeout=30,
        )

    body = {
        "message": case["input"],
        "projectId": project_id,
        "envId": env_id,
        "appName": app_name,
    }
    if args.trace:
        body["trace"] = True

    record = {
        "tc_id": case["id"],
        "repeat": repeat,
        "ok": False,
        "input": case["input"],
        "scope": {"projectId": project_id, "envId": env_id, "appName": app_name},
        "final_text": "",
        "tools_called": [],
        "confirm_cards": [],
        "error": None,
        "latency_ms": 0,
        "event_count": 0,
        "trace": None,
        "budget_stop": False,
        "transport_error": "",
    }

    pending = None
    attempt = 0
    while True:
        try:
            pending = stream_turn("%s/agent/chat" % base, token, body, collector, started_at, args.timeout)
            break
        except DailyCapReached:
            raise
        except (urllib.error.URLError, urllib.error.HTTPError, socket.timeout, OSError) as exc:
            if isinstance(exc, urllib.error.HTTPError) and exc.code < 500:
                record["transport_error"] = "HTTP %d" % exc.code
                record["latency_ms"] = int((time.monotonic() - started_at) * 1000)
                return record
            if attempt >= args.retries:
                record["transport_error"] = str(exc)
                record["latency_ms"] = int((time.monotonic() - started_at) * 1000)
                return record
            time.sleep(RETRY_BACKOFF_SEC[min(attempt, len(RETRY_BACKOFF_SEC) - 1)])
            attempt += 1

    rounds = 0
    while pending is not None:
        if rounds >= MAX_CONFIRM_ROUNDS:
            record["budget_stop"] = True
            decision = "reject"
        else:
            decision = decide(case, args.allow_writes)
        card = {
            "tool_name": pending.get("tool_name", ""),
            "args": pending.get("args", {}),
            "summary": pending.get("summary", ""),
            "price_rub": pending.get("price_rub"),
            "decision": decision,
        }
        collector.confirm_cards.append(card)
        confirm_body = {"action_id": pending["action_id"], "decision": decision}
        if args.trace:
            confirm_body["trace"] = True
        try:
            pending = stream_turn(
                "%s/agent/chat/confirm" % base, token, confirm_body, collector, started_at, args.timeout
            )
        except DailyCapReached:
            raise
        except (urllib.error.URLError, socket.timeout, OSError) as exc:
            record["transport_error"] = str(exc)
            break
        rounds += 1
        if record["budget_stop"]:
            break

    record["ok"] = collector.error is None and not record["transport_error"]
    record["final_text"] = collector.final_text
    record["tools_called"] = collector.tools_called
    record["confirm_cards"] = collector.confirm_cards
    record["error"] = collector.error
    record["trace"] = collector.trace
    record["event_count"] = len(collector.events)
    record["latency_ms"] = int((time.monotonic() - started_at) * 1000)
    return record


def main() -> int:
    here = Path(__file__).resolve().parent
    root = here.parents[1]
    parser = argparse.ArgumentParser(description="Run the agent eval dataset against a live console.")
    parser.add_argument("--dataset", default=str(here / "dataset.jsonl"))
    parser.add_argument("--out-dir", default=str(here / "runs"))
    parser.add_argument("--only", default="", help="comma-separated TC ids to run")
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--concurrency", type=int, default=1)
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--retries", type=int, default=2)
    parser.add_argument("--allow-writes", action="store_true", help="allow approving confirm cards (spends money)")
    parser.add_argument("--trace", action="store_true", help="ask the backend for the trace SSE event")
    parser.add_argument("--no-clear", action="store_true", help="do not clear agent context between cases")
    args = parser.parse_args()

    token = bearer()
    base = api_base()

    cases = load_jsonl(args.dataset)
    if args.only:
        wanted = {x.strip() for x in args.only.split(",") if x.strip()}
        cases = [c for c in cases if c["id"] in wanted]
    if not cases:
        eprint("no cases selected")
        return 1

    scopes = load_scopes()
    if not scopes or not scopes[0]["projectId"]:
        eprint("set DADA_PROJECT_ID and DADA_ENV_ID (or DADA_EVAL_SCOPES) before running")
        return 1
    if args.concurrency > len(scopes):
        eprint(
            "concurrency %d needs %d scopes: the agent keeps one history per (user, project, env),\n"
            "so parallel cases in one scope poison each other. Set DADA_EVAL_SCOPES to a JSON array\n"
            'like [{"projectId":"...","envId":"..."}, ...].' % (args.concurrency, args.concurrency)
        )
        return 1

    run_dir = Path(args.out_dir) / utc_stamp()
    run_dir.mkdir(parents=True, exist_ok=True)

    jobs = []
    for repeat in range(args.repeats):
        for idx, case in enumerate(cases):
            jobs.append((case, scopes[idx % len(scopes)], repeat))

    by_scope = {}
    for job in jobs:
        by_scope.setdefault(job[1]["projectId"] + "|" + job[1]["envId"], []).append(job)

    results = []
    lock = threading.Lock()
    stop = threading.Event()

    def worker(queue):
        out = []
        for case, scope, repeat in queue:
            if stop.is_set():
                break
            try:
                record = run_case(case, scope, repeat, args, token, base)
            except DailyCapReached as exc:
                eprint("daily cap reached: %s" % exc)
                stop.set()
                break
            out.append(record)
            status = "ok" if record["ok"] else "FAIL"
            print(
                "%s r%d %s tools=%d cards=%d %dms"
                % (
                    case["id"],
                    repeat,
                    status,
                    len(record["tools_called"]),
                    len(record["confirm_cards"]),
                    record["latency_ms"],
                )
            )
        with lock:
            results.extend(out)

    started = datetime.now(timezone.utc).isoformat()
    queues = list(by_scope.values())
    with ThreadPoolExecutor(max_workers=max(1, args.concurrency)) as pool:
        list(pool.map(worker, queues))

    results.sort(key=lambda r: (r["tc_id"], r["repeat"]))
    write_jsonl(run_dir / "results.jsonl", results)

    dataset_bytes = Path(args.dataset).read_bytes()
    meta = {
        "started_at": started,
        "finished_at": datetime.now(timezone.utc).isoformat(),
        "api_base": base,
        "dataset": str(args.dataset),
        "dataset_sha256": sha256(dataset_bytes).hexdigest(),
        "git_sha": git_sha(root),
        "repeats": args.repeats,
        "concurrency": args.concurrency,
        "allow_writes": bool(args.allow_writes),
        "trace_requested": bool(args.trace),
        "trace_seen": any(r.get("trace") for r in results),
        "cases_total": len(jobs),
        "cases_failed": sum(1 for r in results if not r["ok"]),
        "aborted": stop.is_set(),
    }
    (run_dir / "meta.json").write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")

    print("run written to %s" % run_dir)
    if stop.is_set():
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
