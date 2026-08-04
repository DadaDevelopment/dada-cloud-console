#!/usr/bin/env python3
"""Attach a judged run's scores to the Langfuse traces of the same turns.

The harness already writes judged.jsonl next to results.jsonl, but a score in a
local file answers "did the run pass" and nothing else. The same score sitting
on the trace answers "why did this case fail" -- one click from a 0 in
`grounding` to the prompt, the tool calls and the arguments that produced it.

The link is the turn's own trace id: run_eval.py --trace records it from the
SSE trace event, and it IS the Langfuse trace id (see agent_chat_trace.go), so
nothing has to be correlated by timestamp. A run made without --trace has no
ids to attach to and this script refuses it rather than inventing traces.

Score ids are derived from (trace id, score name), so re-judging a run and
pushing again overwrites the previous verdict instead of stacking a second one
next to it.

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
from datetime import datetime, timezone
from pathlib import Path
from uuid import uuid5, NAMESPACE_URL

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import eprint, load_jsonl

DEFAULT_HOST = "https://cloud.langfuse.com"

INGEST_PATH = "/api/public/ingestion"

CRITERIA = ("grounding", "action", "safety", "navigation", "tone")

BATCH_SIZE = 100


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


def score_id(trace_id, name):
    return str(uuid5(NAMESPACE_URL, "dada-eval/%s/%s" % (trace_id, name)))


def score_event(trace_id, name, value, data_type, comment, stamp):
    body = {
        "id": score_id(trace_id, name),
        "traceId": trace_id,
        "name": name,
        "value": value,
        "dataType": data_type,
    }
    if comment:
        body["comment"] = str(comment)[:1000]
    return {"id": str(uuid5(NAMESPACE_URL, body["id"])), "type": "score-create", "timestamp": stamp, "body": body}


def events_for(judged, trace_id, stamp):
    """Every score one judged case contributes: criteria, total, verdict, gates."""
    out = []
    for criterion in CRITERIA:
        entry = judged["scores"].get(criterion) or {}
        if entry.get("score") is None:
            continue
        out.append(score_event(trace_id, criterion, entry["score"], "NUMERIC", entry.get("evidence", ""), stamp))

    if judged.get("total") is not None:
        out.append(score_event(trace_id, "total", judged["total"], "NUMERIC", "; ".join(judged.get("gate_notes") or []), stamp))

    if judged.get("passed") is not None:
        out.append(score_event(trace_id, "passed", 1 if judged["passed"] else 0, "BOOLEAN", judged.get("transport_error", ""), stamp))

    gates = judged.get("gates") or {}
    if gates.get("safety_violation") is not None:
        out.append(
            score_event(
                trace_id,
                "safety_violation",
                1 if gates["safety_violation"] else 0,
                "BOOLEAN",
                gates.get("write_without_card") or "",
                stamp,
            )
        )
    return out


def post_batch(host, public, secret, batch, timeout):
    payload = json.dumps({"batch": batch}).encode("utf-8")
    req = urllib.request.Request(host + INGEST_PATH, data=payload, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Basic " + b64encode(("%s:%s" % (public, secret)).encode("utf-8")).decode("ascii"))
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            parsed = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        return 0, ["HTTP %d: %s" % (exc.code, exc.read().decode("utf-8", "replace")[:400])]
    except Exception as exc:
        return 0, [str(exc)]

    errors = []
    for item in parsed.get("errors") or []:
        errors.append("%s: %s %s" % (item.get("id"), item.get("message"), str(item.get("error"))[:300]))
    return len(parsed.get("successes") or []), errors


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

    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.000Z")
    batch = []
    matched = 0
    missing = []
    for judged in load_jsonl(judged_path):
        key = (judged["tc_id"], judged.get("repeat", 0))
        trace_id = trace_ids.get(key)
        if not trace_id:
            missing.append("%s r%s" % key)
            continue
        matched += 1
        batch.extend(events_for(judged, trace_id, stamp))

    if missing:
        eprint("%d judged case(s) had no trace id and were skipped: %s" % (len(missing), ", ".join(missing[:10])))

    if not batch:
        eprint("nothing to push")
        return 1

    if args.dry_run:
        print(json.dumps({"batch": batch}, ensure_ascii=False, indent=2))
        print("dry run: %d score(s) for %d case(s), nothing sent" % (len(batch), matched))
        return 0

    host, public, secret = credentials()
    sent = 0
    failures = []
    for start in range(0, len(batch), BATCH_SIZE):
        ok, errors = post_batch(host, public, secret, batch[start : start + BATCH_SIZE], args.timeout)
        sent += ok
        failures.extend(errors)

    print("pushed %d/%d score(s) for %d case(s) to %s" % (sent, len(batch), matched, host))
    if failures:
        eprint("%d score(s) rejected:" % len(failures))
        for failure in failures[:10]:
            eprint("  " + failure)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
