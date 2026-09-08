"""Anti-slop gate for the vibecoder persona: agentkit rules plus freshness rules.

Two layers, deliberately separate. ``agentkit.humanize`` owns everything that
is true for any human-sounding agent (banned phrases, markdown in a chat
reply, typography tells, length). This module owns what is true only for an
agent that talks about a fast-moving field: a bare version number, price, or
context-window figure stated without having looked it up is the single failure
mode that turns the bot from useful into embarrassing in public.

The check is textual and therefore approximate: it cannot know whether the
model actually called ``news_search``. It flags the shape of an unsourced
claim, and the eval runner pairs it with the tool-call trace when one exists.

Exit code 1 on any critical finding, so it drops straight into a pre-merge
hook or a Jenkins stage.
"""

import argparse
import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "agentkit"))

import humanize

VERSION_CLAIM_RE = re.compile(
    r"\b(gpt|claude|gemini|llama|qwen|deepseek|grok|mistral|glm|o\d)[\s-]?[\w.]*\s?"
    r"(\d+(?:\.\d+)?)\b",
    re.I,
)
PRICE_RE = re.compile(r"\$\s?\d|\d+\s?(?:\$|долл|руб|₽)\s?(?:за|/)\s?(?:млн|1m|m|токен)", re.I)
WINDOW_RE = re.compile(r"\b\d{2,4}\s?(?:k|к|тыс)\s?(?:токен|context|контекст)", re.I)
_RECENCY_WORDS = r"(?:вчера|сегодня|позавчера|на\s+прошлой\s+неделе|только\s+что|на\s+днях)"
_RELEASE_VERBS = r"(?:вышел|вышла|вышло|выпустил\w*|релизн\w+|анонсиров\w+|появил\w+|завезл\w+|выкатил\w+)"
DATE_CLAIM_RE = re.compile(
    rf"{_RECENCY_WORDS}[^.!?]{{0,40}}?{_RELEASE_VERBS}|{_RELEASE_VERBS}[^.!?]{{0,40}}?{_RECENCY_WORDS}",
    re.I,
)

HEDGE_MARKERS = (
    "не проверял",
    "не смотрел",
    "могу ошибаться",
    "по состоянию на",
    "проверил",
    "судя по",
    "по данным",
)

IMPERSONATION_RE = re.compile(r"\bя\s+(?:человек|не\s+бот|живой)\b", re.I)


def freshness_findings(text: str, sourced: bool = False) -> list[humanize.Finding]:
    """Flag volatile claims stated flat.

    ``sourced`` is set by the eval runner when the turn actually called the
    news tool; in that case a version number is a reported fact rather than a
    memorized one and the rule stands down.
    """
    findings: list[humanize.Finding] = []
    if sourced:
        return findings
    low = text.lower()
    hedged = any(m in low for m in HEDGE_MARKERS)
    if hedged:
        return findings
    m = VERSION_CLAIM_RE.search(text)
    if m:
        findings.append(humanize.Finding("unsourced_version", "critical", m.group(0).strip()))
    if PRICE_RE.search(text):
        findings.append(humanize.Finding("unsourced_price", "critical", "цифра цены без источника"))
    if WINDOW_RE.search(text):
        findings.append(humanize.Finding("unsourced_limit", "critical", "размер контекста без источника"))
    if DATE_CLAIM_RE.search(text):
        findings.append(humanize.Finding("unsourced_recency", "critical", "заявка о свежести без источника"))
    return findings


def identity_findings(text: str) -> list[humanize.Finding]:
    """The persona may be casual, but it may not claim to be a human."""
    if IMPERSONATION_RE.search(text):
        return [humanize.Finding("false_human_claim", "critical", "отрицание того, что это бот")]
    return []


def lint(text: str, sourced: bool = False) -> dict:
    findings = humanize.inspect(text) + freshness_findings(text, sourced) + identity_findings(text)
    critical = [f.as_dict() for f in findings if f.severity == "critical"]
    soft = [f.as_dict() for f in findings if f.severity == "soft"]
    return {"clean": not critical, "critical": critical, "soft": soft}


def style_failures(text: str, sourced: bool = False) -> list[str]:
    """Adapter for agentkit.evalspec.score_case."""
    return [f"{f['rule']}: {f['detail']}" for f in lint(text, sourced)["critical"]]


def main() -> int:
    ap = argparse.ArgumentParser(description="lint one reply or a JSONL of replies")
    ap.add_argument("path", nargs="?", help="file with the reply; omit to read stdin")
    ap.add_argument("--jsonl", action="store_true", help="treat input as JSONL with a 'reply' field")
    ap.add_argument("--sourced", action="store_true", help="the turn called news_search")
    ap.add_argument("--json", action="store_true", dest="as_json")
    args = ap.parse_args()

    raw = open(args.path, encoding="utf-8").read() if args.path else sys.stdin.read()

    if args.jsonl:
        rows = []
        failed = 0
        for line in raw.splitlines():
            if not line.strip():
                continue
            obj = json.loads(line)
            result = lint(obj.get("reply", ""), obj.get("sourced", args.sourced))
            result["id"] = obj.get("id")
            rows.append(result)
            failed += 0 if result["clean"] else 1
        print(json.dumps(rows, ensure_ascii=False, indent=2) if args.as_json
              else f"{len(rows) - failed}/{len(rows)} чисто")
        return 1 if failed else 0

    result = lint(raw, args.sourced)
    if args.as_json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        if result["clean"]:
            print("чисто")
        for f in result["critical"]:
            print(f"CRITICAL {f['rule']}: {f['detail']}")
        for f in result["soft"]:
            print(f"soft     {f['rule']}: {f['detail']}")
    return 0 if result["clean"] else 1


if __name__ == "__main__":
    sys.exit(main())
