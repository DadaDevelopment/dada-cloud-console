#!/usr/bin/env python3
"""Attach a judged run's scores to the Langfuse traces of the same turns.

The harness already writes judged.jsonl next to results.jsonl, but a score in a
local file answers "did the run pass" and nothing else. The same score sitting
on the trace answers "why did this case fail" -- one click from a 0 in
`grounding` to the prompt, the tool calls and the arguments that produced it.

The link is the turn's own trace id: run_eval.py --trace records it from the
SSE trace event, and the console ships it to Langfuse over OTLP (see
agent_chat_trace.go and langfuse/otlp.go), where a UUID becomes the same 16
bytes as 32 hex digits. That mapping is repeated here so nothing has to be
correlated by timestamp. A run made without --trace has no ids to attach to
and this script refuses it rather than inventing traces.

Score ids are derived from (trace id, score name), so re-judging a run and
pushing again overwrites the previous verdict instead of stacking a second one
next to it. Scores go through POST /api/public/scores one at a time: the batch
ingestion endpoint is the legacy v3 path and is closed to newer organisations.

    export LANGFUSE_PUBLIC_KEY=... LANGFUSE_SECRET_KEY=...
    python3 scripts/agent-eval/push_scores.py runs/20260803-101500
    python3 scripts/agent-eval/push_scores.py runs/... --dry-run
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from base64 import b64encode
from hashlib import sha256
from pathlib import Path
from uuid import uuid5, NAMESPACE_URL

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import eprint, load_jsonl

DEFAULT_HOST = "https://cloud.langfuse.com"

SCORES_PATH = "/api/public/scores"

CRITERIA = ("grounding", "action", "safety", "navigation", "tone")


def credentials():
    """Langfuse keys from the environment, or exit with an explanation."""
    public = os.environ.get("LANGFUSE_PUBLIC_KEY", "").strip()
    secret = os.environ.get("LANGFUSE_SECRET_KEY", "").strip()
    if not public or not secret:
        eprint(
            "LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY are not set.\n"
            "They are the same project keys the console backend uses "
            "(Secret dada-cloud-console-backend)."
        )
        raise SystemExit(1)
    host = os.environ.get("LANGFUSE_HOST", "").strip() or os.environ.get("LANGFUSE_BASE_URL", "").strip() or DEFAULT_HOST
    return host.rstrip("/"), public, secret


def langfuse_trace_id(trace_id):
    """The id Langfuse stores for a trace the console sent over OTLP.

    Mirrors otlpTraceID in backend/internal/langfuse/otlp.go: a UUID keeps its
    bytes and loses the dashes, anything else is hashed down to 16 bytes.
    """
    compact = trace_id.replace("-", "").lower()
    if len(compact) == 32 and all(c in "0123456789abcdef" for c in compact):
        return compact
    return sha256(trace_id.encode("utf-8")).hexdigest()[:32]


def score_id(trace_id, name):
    return str(uuid5(NAMESPACE_URL, "dada-eval/%s/%s" % (trace_id, name)))


def score_body(trace_id, name, value, data_type, comment):
    body = {
        "id": score_id(trace_id, name),
        "traceId": trace_id,
        "name": name,
        "value": value,
        "dataType": data_type,
    }
    if comment:
        body["comment"] = str(comment)[:1000]
    return body


def scores_for(judged, trace_id):
    """Every score one judged case contributes: criteria, total, verdict, gates."""
    out = []
    for criterion in CRITERIA:
        entry = judged["scores"].get(criterion) or {}
        if entry.get("score") is None:
            continue
        out.append(score_body(trace_id, criterion, entry["score"], "NUMERIC", entry.get("evidence", "")))

    if judged.get("total") is not None:
        out.append(score_body(trace_id, "total", judged["total"], "NUMERIC", "; ".join(judged.get("gate_notes") or [])))

    if judged.get("passed") is not None:
        out.append(score_body(trace_id, "passed", 1 if judged["passed"] else 0, "BOOLEAN", judged.get("transport_error", "")))

    gates = judged.get("gates") or {}
    if gates.get("safety_violation") is not None:
        out.append(
            score_body(
                trace_id,
                "safety_violation",
                1 if gates["safety_violation"] else 0,
                "BOOLEAN",
                gates.get("write_without_card") or "",
            )
        )
    return out


def post_score(host, public, secret, score, timeout):
    payload = json.dumps(score).encode("utf-8")
    req = urllib.request.Request(host + SCORES_PATH, data=payload, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Basic " + b64encode(("%s:%s" % (public, secret)).encode("utf-8")).decode("ascii"))
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            resp.read()
    except urllib.error.HTTPError as exc:
        return "%s/%s: HTTP %d: %s" % (score["traceId"], score["name"], exc.code, exc.read().decode("utf-8", "replace")[:400])
    except Exception as exc:
        return "%s/%s: %s" % (score["traceId"], score["name"], exc)
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="Push judged eval scores onto their Langfuse traces.")
    parser.add_argument("run_dir", help="a directory produced by run_eval.py and judged by judge.py")
    parser.add_argument("--timeout", type=int, default=30)
    parser.add_argument("--dry-run", action="store_true", help="build the batch and print it, send nothing")
    args = parser.parse_args()

    run_dir = Path(args.run_dir)
    judged_path = run_dir / "judged.jsonl"
    results_path = run_dir / "results.jsonl"
    for path in (judged_path, results_path):
        if not path.exists():
            eprint("no %s in %s" % (path.name, run_dir))
            return 1

    trace_ids = {}
    for result in load_jsonl(results_path):
        trace = result.get("trace") or {}
        if trace.get("trace_id"):
            trace_ids[(result["tc_id"], result.get("repeat", 0))] = trace["trace_id"]

    if not trace_ids:
        eprint(
            "this run carries no trace ids, so there is nothing to attach scores to.\n"
            "Re-run it with: python3 scripts/agent-eval/run_eval.py --trace"
        )
        return 1

    scores = []
    matched = 0
    missing = []
    for judged in load_jsonl(judged_path):
        key = (judged["tc_id"], judged.get("repeat", 0))
        trace_id = trace_ids.get(key)
        if not trace_id:
            missing.append("%s r%s" % key)
            continue
        matched += 1
        scores.extend(scores_for(judged, langfuse_trace_id(trace_id)))

    if missing:
        eprint("%d judged case(s) had no trace id and were skipped: %s" % (len(missing), ", ".join(missing[:10])))

    if not scores:
        eprint("nothing to push")
        return 1

    if args.dry_run:
        print(json.dumps({"scores": scores}, ensure_ascii=False, indent=2))
        print("dry run: %d score(s) for %d case(s), nothing sent" % (len(scores), matched))
        return 0

    host, public, secret = credentials()
    sent = 0
    failures = []
    for score in scores:
        error = post_score(host, public, secret, score, args.timeout)
        if error:
            failures.append(error)
        else:
            sent += 1

    print("pushed %d/%d score(s) for %d case(s) to %s" % (sent, len(scores), matched, host))
    if failures:
        eprint("%d score(s) rejected:" % len(failures))
        for failure in failures[:10]:
            eprint("  " + failure)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
