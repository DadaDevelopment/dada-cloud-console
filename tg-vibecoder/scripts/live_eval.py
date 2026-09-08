"""Score the deployed agent, not the model it is built from.

scripts/bakeoff.py answers "which model writes this persona best" by talking
straight to a chat/completions endpoint. That is a model measurement: it has no
kagent runtime, no MCP tool server, no system prompt assembled by the operator,
no reply budget. Every one of those can change what reaches the chat, and none
of them is exercised by a bakeoff, so a green bakeoff and a broken deployment
look identical from here.

This runner sends the same golden cases through the console's agent endpoint,
which is the same A2A hop the telegram gateway makes. What it grades is what a
commenter would receive.

Two honest limits, stated because they change how the number reads:

- The A2A reply carries no tool trace, so this runner cannot know whether a
  version number was looked up or remembered. Freshness findings are therefore
  reported as ``source unknown`` and counted separately instead of being waved
  through.
- The agent is stateless per call here, as it is in the gateway's per-message
  turn, so a case that depends on what was said earlier is out of scope.

Usage:
    TOKEN=$(bash automator/state/get-mcp-token.sh) \
    python3 scripts/live_eval.py --agent tg-vibecoder --split holdout
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
PROJECT = os.path.dirname(HERE)
REPO = os.path.dirname(PROJECT)
sys.path.insert(0, os.path.join(REPO, "agentkit"))
sys.path.insert(0, HERE)

import evalspec
import transcript
import persona_lint

DEFAULT_CASES = os.path.join(PROJECT, "evals", "persona", "cases.jsonl")
DEFAULT_BASE = "https://console.dada-tuda.ru"

FRESHNESS_RULES = ("unsourced_version", "unsourced_price", "unsourced_limit", "unsourced_recency")


def render_case(case: dict) -> str:
    """The envelope the gateway builds, byte for byte."""
    speaker = transcript.speaker(case.get("speaker", ""), case.get("speaker_username", ""))
    quoted = transcript.quoted_context(case.get("post", ""), is_channel=True)
    return transcript.inbound(case["incoming"], speaker, quoted)


def send(base: str, token: str, agent: str, text: str, timeout: float) -> str:
    url = f"{base.rstrip('/')}/api/v1/agents/{agent}/message"
    body = json.dumps({"text": text}).encode()
    req = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return (json.loads(resp.read().decode()).get("reply") or "").strip()


def split_failures(failures: list[str]) -> tuple[list[str], list[str]]:
    """Separate "wrong" from "cannot tell without a tool trace"."""
    hard, unknown = [], []
    for f in failures:
        (unknown if f.split(":", 1)[0] in FRESHNESS_RULES else hard).append(f)
    return hard, unknown


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--agent", required=True)
    ap.add_argument("--cases", default=DEFAULT_CASES)
    ap.add_argument("--split", default="holdout", choices=["dev", "holdout"])
    ap.add_argument("--base", default=os.environ.get("DADA_CONSOLE", DEFAULT_BASE))
    ap.add_argument("--threshold", type=float, default=0.75)
    ap.add_argument("--timeout", type=float, default=120.0)
    ap.add_argument("--sleep", type=float, default=1.0)
    ap.add_argument("--out")
    args = ap.parse_args()

    token = os.environ.get("TOKEN", "")
    if not token:
        print("env TOKEN пуст: TOKEN=$(bash automator/state/get-mcp-token.sh)", file=sys.stderr)
        return 2

    cases = evalspec.load_cases(args.cases, args.split)
    if not cases:
        print(f"нет кейсов в сплите {args.split}", file=sys.stderr)
        return 2

    latencies: list[float] = []

    def respond(case: dict) -> str:
        started = time.monotonic()
        try:
            reply = send(args.base, token, args.agent, render_case(case), args.timeout)
        finally:
            latencies.append(time.monotonic() - started)
        if args.sleep:
            time.sleep(args.sleep)
        return reply

    print(f"агент {args.agent} на {args.base}, кейсов: {len(cases)} ({args.split})\n", flush=True)
    results = evalspec.run_suite(cases, respond, lambda r: persona_lint.style_failures(r, sourced=False))

    rows = []
    hard_passed = 0
    unknown_only = 0
    for r in results:
        hard, unknown = split_failures(r.failures)
        if not hard:
            hard_passed += 1
            if unknown:
                unknown_only += 1
        rows.append({**r.as_dict(), "hard": hard, "source_unknown": unknown})

    total = len(results)
    rate = round(hard_passed / total, 3) if total else 0.0
    p50 = round(sorted(latencies)[len(latencies) // 2], 2) if latencies else None
    print(f"прошло {hard_passed}/{total} = {rate}, p50 {p50}s, max {round(max(latencies), 2)}s")
    if unknown_only:
        print(f"из них {unknown_only} с находкой о свежести, источник неизвестен без трейса тула")

    for row in rows:
        if row["hard"]:
            print(f"  {row['case_id']}: {'; '.join(row['hard'])[:160]}")
    for row in rows:
        if row["source_unknown"] and not row["hard"]:
            print(f"  ~ {row['case_id']}: {'; '.join(row['source_unknown'])[:160]}")

    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            json.dump({"agent": args.agent, "split": args.split, "rate": rate, "rows": rows}, fh,
                      ensure_ascii=False, indent=2)
        print(f"\nполный лог: {args.out}")

    ok = rate >= args.threshold
    print(f"\nживой рантайм: {rate} (порог {args.threshold}) -> {'PASS' if ok else 'FAIL'}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
