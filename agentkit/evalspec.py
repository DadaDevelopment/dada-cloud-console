"""Golden-set eval format and runner shared by every agent in this repo.

Platform track 1 (эвалы в деплой-цикле). The contract is a JSONL file of
cases, one JSON object per line, so a case set diffs cleanly in review and a
client repo can own its own file without importing any of our code.

Case schema (``validate_case`` is the authority):

    id            unique string
    split         "dev" | "holdout"
    incoming      the user message the agent sees
    post          optional surrounding context (the channel post, a ticket)
    expect.should_reply   bool, whether silence is the correct outcome
    expect.domain         optional skill/domain the agent should have loaded
    expect.must_not_contain  list of substrings that fail the case outright
    expect.rubric         human-readable pass criterion for the judge pass

The split is not decoration. Prompt edits are tuned against ``dev``; the
``holdout`` set is scored once per candidate and is the only number allowed
in a ship/no-ship decision. Tuning against holdout turns the eval into a
mirror.

The runner is transport-agnostic: pass any callable ``str -> str`` (or
``dict -> str``). That keeps the same file usable for a local model bakeoff,
a live A2A agent, and a CI gate.
"""

import json
from collections.abc import Callable, Iterable

REQUIRED_TOP = ("id", "split", "incoming", "expect")
SPLITS = ("dev", "holdout")


class CaseError(ValueError):
    """A case that cannot be scored. Raised at load time, never at run time."""


def validate_case(case: dict, seen_ids: set[str] | None = None) -> None:
    for key in REQUIRED_TOP:
        if key not in case:
            raise CaseError(f"case missing {key!r}: {case.get('id', '<no id>')}")
    if case["split"] not in SPLITS:
        raise CaseError(f"case {case['id']}: split must be one of {SPLITS}")
    expect = case["expect"]
    if not isinstance(expect, dict):
        raise CaseError(f"case {case['id']}: expect must be an object")
    if "should_reply" not in expect:
        raise CaseError(f"case {case['id']}: expect.should_reply is required")
    if not isinstance(expect["should_reply"], bool):
        raise CaseError(f"case {case['id']}: expect.should_reply must be a bool")
    for field in ("must_not_contain",):
        if field in expect and not isinstance(expect[field], list):
            raise CaseError(f"case {case['id']}: expect.{field} must be a list")
    if "allow_silence" in expect:
        if not isinstance(expect["allow_silence"], bool):
            raise CaseError(f"case {case['id']}: expect.allow_silence must be a bool")
        if expect["allow_silence"] and not expect["should_reply"]:
            raise CaseError(
                f"case {case['id']}: allow_silence is meaningless when should_reply is false"
            )
    if "allow_reply" in expect:
        if not isinstance(expect["allow_reply"], bool):
            raise CaseError(f"case {case['id']}: expect.allow_reply must be a bool")
        if expect["allow_reply"] and expect["should_reply"]:
            raise CaseError(
                f"case {case['id']}: allow_reply is meaningless when should_reply is true"
            )
    if seen_ids is not None:
        if case["id"] in seen_ids:
            raise CaseError(f"duplicate case id: {case['id']}")
        seen_ids.add(case["id"])


def load_cases(path: str, split: str | None = None) -> list[dict]:
    """Read and validate a JSONL case file. Blank lines are ignored."""
    cases: list[dict] = []
    seen: set[str] = set()
    with open(path, encoding="utf-8") as fh:
        for lineno, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            try:
                case = json.loads(line)
            except json.JSONDecodeError as exc:
                raise CaseError(f"{path}:{lineno}: {exc}") from exc
            validate_case(case, seen)
            if split is None or case["split"] == split:
                cases.append(case)
    return cases


class CaseResult:
    """Outcome of one case: the reply produced and every reason it failed."""

    __slots__ = ("case_id", "split", "reply", "failures")

    def __init__(self, case_id: str, split: str, reply: str, failures: list[str]):
        self.case_id = case_id
        self.split = split
        self.reply = reply
        self.failures = failures

    @property
    def passed(self) -> bool:
        return not self.failures

    def as_dict(self) -> dict:
        return {
            "case_id": self.case_id,
            "split": self.split,
            "passed": self.passed,
            "failures": self.failures,
            "reply": self.reply,
        }


SILENCE_TOKENS = {"", "<skip>", "[skip]", "skip", "<молчу>"}


def score_case(case: dict, reply: str, style_check: Callable[[str], list[str]] | None = None) -> CaseResult:
    """Score one reply against one case using only mechanical checks.

    ``expect.allow_silence`` marks a case where both answering and staying
    quiet are correct (a troll, a provocation): silence passes, and a reply
    still has to survive every content and style check. It is rejected on a
    ``should_reply: false`` case, where it would mean nothing.

    ``expect.allow_reply`` is its mirror on a ``should_reply: false`` case:
    silence is preferred, a short reply is tolerated, and that reply still
    faces every content and style check. Reach for it only when the rubric
    itself offers the agent a choice. A rubric that says "stays quiet or one
    neutral line" while the flag says silence-only is a defect in the case,
    not in the agent, and it will read as a model failure forever.

    ``style_check`` returns a list of failure strings for a non-empty reply;
    pass ``agentkit.humanize`` critical findings here. A judge model, if used
    at all, runs on top of this and never replaces it: a reply that fails a
    mechanical check is already wrong regardless of what a judge thinks.
    """
    failures: list[str] = []
    expect = case["expect"]
    normalized = reply.strip()
    stayed_silent = normalized.lower() in SILENCE_TOKENS

    if expect["should_reply"] and stayed_silent and not expect.get("allow_silence"):
        failures.append("expected a reply, got silence")
    if not expect["should_reply"] and not stayed_silent and not expect.get("allow_reply"):
        failures.append("expected silence, got a reply")

    if not stayed_silent:
        low = normalized.lower()
        for banned in expect.get("must_not_contain", []):
            if banned.lower() in low:
                failures.append(f"contains forbidden substring: {banned!r}")
        if style_check is not None:
            failures.extend(style_check(normalized))

    return CaseResult(case["id"], case["split"], reply, failures)


def run_suite(
    cases: Iterable[dict],
    respond: Callable[[dict], str],
    style_check: Callable[[str], list[str]] | None = None,
) -> list[CaseResult]:
    """Run every case through ``respond``; a raised exception fails that case only."""
    results: list[CaseResult] = []
    for case in cases:
        try:
            reply = respond(case)
        except Exception as exc:
            results.append(CaseResult(case["id"], case["split"], "", [f"runner error: {exc}"]))
            continue
        results.append(score_case(case, reply, style_check))
    return results


def summarize(results: list[CaseResult]) -> dict:
    by_split: dict[str, dict] = {}
    for r in results:
        bucket = by_split.setdefault(r.split, {"total": 0, "passed": 0, "failed_ids": []})
        bucket["total"] += 1
        if r.passed:
            bucket["passed"] += 1
        else:
            bucket["failed_ids"].append(r.case_id)
    for bucket in by_split.values():
        bucket["rate"] = round(bucket["passed"] / bucket["total"], 3) if bucket["total"] else 0.0
    total = len(results)
    passed = sum(1 for r in results if r.passed)
    return {
        "total": total,
        "passed": passed,
        "rate": round(passed / total, 3) if total else 0.0,
        "by_split": by_split,
    }


def gate(summary: dict, threshold: float, split: str = "holdout") -> tuple[bool, str]:
    """Ship/no-ship on one split against a threshold declared before the run."""
    bucket = summary["by_split"].get(split)
    if not bucket:
        return False, f"split {split!r} has no cases"
    ok = bucket["rate"] >= threshold
    verdict = "PASS" if ok else "FAIL"
    return ok, f"{verdict} {split}: {bucket['passed']}/{bucket['total']} = {bucket['rate']} (порог {threshold})"
