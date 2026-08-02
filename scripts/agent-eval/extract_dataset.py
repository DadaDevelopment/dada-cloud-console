#!/usr/bin/env python3
"""Turn the benchmark spec into a machine-readable dataset.

Parses ``docs/product/agent-eval-personas-and-cases.md`` section 3 into
``dataset.jsonl`` -- one JSON object per test case, with the axes, rubric,
anti-pattern and the derived tool expectations the judge and the report need.

    python3 scripts/agent-eval/extract_dataset.py --verify-tools --expect 38
"""

import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import (
    ALL_TOOLS,
    DENY_TOOLS,
    INVENTORY_PROBLEMS,
    READ_TOOLS,
    TOOL_RENAMES,
    WRITE_TOOLS,
    inventory_drift,
    repo_root,
    write_jsonl,
)

CASE_HEADER = re.compile(r"^### (TC-\d+): (.+?)\s*$")
FIELD = re.compile(r"^- \*\*(.+?):\*\*\s?(.*)$")
BACKTICK = re.compile(r"`([^`]+)`")
QUOTED = re.compile(r"«(.+?)»", re.S)

FIELD_KEYS = {
    "Персона": "persona_label",
    "Оси": "axes_raw",
    "Стартовое состояние": "start_state",
    "Ввод юзера (verbatim)": "input_raw",
    "Правильное поведение (rubric)": "rubric",
    "Anti-pattern (fail)": "anti_pattern",
    "Judge-критерии": "judge_criteria",
    "Вес": "weight",
}

WEIGHT_NUM = {"critical": 3, "high": 2, "medium": 1}

LIST_FIELDS = (
    "expected_tools",
    "forbidden_tools",
    "must_not_deny",
    "must_not_invent",
    "required_nav_path",
    "persona",
)


def split_cases(text: str):
    """Yield (tc_id, title, body_lines, source_line) for every ### TC-NN block."""
    lines = text.split("\n")
    starts = []
    for idx, line in enumerate(lines):
        m = CASE_HEADER.match(line)
        if m:
            starts.append((idx, m.group(1), m.group(2)))

    for pos, (idx, tc_id, title) in enumerate(starts):
        end = starts[pos + 1][0] if pos + 1 < len(starts) else len(lines)
        for probe in range(idx + 1, end):
            if lines[probe].startswith("## "):
                end = probe
                break
        yield tc_id, title, lines[idx + 1 : end], idx + 1


def parse_fields(body_lines):
    """Split a case body into named fields, keeping multi-line values together."""
    fields = {}
    current = None
    for line in body_lines:
        m = FIELD.match(line)
        if m:
            label = m.group(1).strip()
            current = FIELD_KEYS.get(label)
            if current is None:
                continue
            fields[current] = [m.group(2)]
            continue
        if current is None:
            continue
        if line.startswith("---"):
            current = None
            continue
        fields[current].append(line)
    return fields


def join_flat(chunk) -> str:
    """Collapse a wrapped field value into a single line."""
    return re.sub(r"\s+", " ", " ".join(chunk)).strip()


def join_rubric(chunk) -> str:
    """Keep the numbered rubric steps on separate lines, unwrap continuations."""
    steps = []
    for raw in chunk:
        line = raw.strip()
        if not line:
            continue
        if re.match(r"^\d+\.\s", line):
            steps.append(line)
        elif steps:
            steps[-1] = steps[-1] + " " + line
        else:
            steps.append(line)
    return "\n".join(steps)


def parse_axes(raw: str):
    """Split the axis line into the six taxonomy coordinates."""
    parts = [p.strip() for p in raw.split("/") if p.strip()]
    if len(parts) != 6:
        return None
    intent, level, destructive, multistep, ambiguity, escalation = parts
    return {
        "intent": [p.strip() for p in intent.split("+") if p.strip()],
        "context_level": level,
        "destructiveness": destructive,
        "multistep": multistep,
        "ambiguity": [p.strip() for p in ambiguity.split("+") if p.strip()],
        "escalation_allowed": escalation,
    }


def tool_tokens(text: str):
    """Tool identifiers mentioned inside backticks, in order of appearance."""
    out = []
    for chunk in BACKTICK.findall(text):
        for token in re.split(r"[\s/,]+", chunk):
            token = token.strip("`.,;:()")
            token = TOOL_RENAMES.get(token, token)
            if token in ALL_TOOLS and token not in out:
                out.append(token)
    return out


def nav_paths(text: str):
    """Console routes mentioned inside backticks, in order of appearance."""
    out = []
    for chunk in BACKTICK.findall(text):
        for token in re.split(r"[\s,;]+", chunk):
            token = token.strip("`.,;:()")
            if token.startswith("/") and token not in out:
                out.append(token)
    return out


def build_case(tc_id, title, body_lines, source_line):
    """Assemble one dataset row from a parsed spec block."""
    fields = parse_fields(body_lines)
    missing = [k for k in ("persona_label", "axes_raw", "input_raw", "rubric", "weight") if k not in fields]
    if missing:
        raise ValueError("%s: missing fields %s" % (tc_id, ", ".join(missing)))

    persona_label = join_flat(fields["persona_label"])
    axes_raw = join_flat(fields["axes_raw"])
    axes = parse_axes(axes_raw)
    if axes is None:
        raise ValueError("%s: cannot parse axes %r" % (tc_id, axes_raw))

    input_raw = join_flat(fields["input_raw"])
    quoted = QUOTED.search(input_raw)
    if not quoted:
        raise ValueError("%s: no verbatim user input found in %r" % (tc_id, input_raw))
    user_input = re.sub(r"\s+", " ", quoted.group(1)).strip()

    rubric = join_rubric(fields["rubric"])
    anti_pattern = join_flat(fields.get("anti_pattern", []))
    judge_criteria = join_flat(fields.get("judge_criteria", []))
    start_state = join_flat(fields.get("start_state", []))
    weight = join_flat(fields["weight"]).lower()

    expected = tool_tokens(rubric)
    forbidden = []
    for name in tool_tokens(anti_pattern):
        if name in WRITE_TOOLS and name not in expected and name not in forbidden:
            forbidden.append(name)

    pre_action = "reject_pending" if "нажатие Reject" in input_raw else ""

    return {
        "id": tc_id,
        "title": title,
        "persona": re.findall(r"P\d+", persona_label),
        "persona_label": persona_label,
        "intent": axes["intent"],
        "context_level": axes["context_level"],
        "destructiveness": axes["destructiveness"],
        "multistep": axes["multistep"],
        "ambiguity": axes["ambiguity"],
        "escalation_allowed": axes["escalation_allowed"],
        "weight": weight,
        "weight_num": WEIGHT_NUM.get(weight, 1),
        "start_state": start_state,
        "input": user_input,
        "input_context": {},
        "rubric": rubric,
        "anti_pattern": anti_pattern,
        "judge_criteria": judge_criteria,
        "expected_tools": expected,
        "forbidden_tools": forbidden,
        "must_not_deny": [],
        "must_not_invent": [],
        "required_nav_path": nav_paths(rubric),
        "confirm_policy": "reject",
        "pre_action": pre_action,
        "source_line": source_line,
    }


def apply_overrides(case, override):
    """Merge a manual override block.

    A key ending in ``+`` appends to the existing list field; every other key
    replaces the value outright, so the override file always reads literally.
    """
    for key, value in override.items():
        if key.endswith("+") and key[:-1] in LIST_FIELDS:
            base = case.setdefault(key[:-1], [])
            for item in value:
                if item not in base:
                    base.append(item)
            continue
        case[key] = value
    return case


def main() -> int:
    root = repo_root()
    here = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Extract the agent eval dataset from the spec.")
    parser.add_argument("--doc", default=str(root / "docs" / "product" / "agent-eval-personas-and-cases.md"))
    parser.add_argument("--out", default=str(here / "dataset.jsonl"))
    parser.add_argument("--overrides", default=str(here / "overrides.json"))
    parser.add_argument("--expect", type=int, default=0, help="fail unless exactly N cases were parsed")
    parser.add_argument("--verify-tools", action="store_true", help="fail on drift against toolset.go")
    args = parser.parse_args()

    if INVENTORY_PROBLEMS:
        for line in INVENTORY_PROBLEMS:
            print("tool inventory: %s" % line, file=sys.stderr)
        return 1

    if args.verify_tools:
        for line in inventory_drift(READ_TOOLS, WRITE_TOOLS, DENY_TOOLS):
            print("tool inventory drift: %s" % line, file=sys.stderr)

    text = Path(args.doc).read_text(encoding="utf-8")
    cases = [build_case(*item) for item in split_cases(text)]

    overrides = {}
    ov_path = Path(args.overrides)
    if ov_path.exists():
        overrides = json.loads(ov_path.read_text(encoding="utf-8"))

    overrides = {k: v for k, v in overrides.items() if not k.startswith("_")}

    known = {c["id"] for c in cases}
    unknown = [k for k in overrides if k not in known]
    if unknown:
        print("overrides reference unknown cases: %s" % ", ".join(sorted(unknown)), file=sys.stderr)
        return 1

    for case in cases:
        if case["id"] in overrides:
            apply_overrides(case, overrides[case["id"]])
        for name in DENY_TOOLS:
            if name not in case["forbidden_tools"]:
                case["forbidden_tools"].append(name)

    problems = []
    for case in cases:
        clash = sorted(set(case["expected_tools"]) & set(case["forbidden_tools"]))
        if clash:
            problems.append("%s: tools are both expected and forbidden: %s" % (case["id"], ", ".join(clash)))
    if problems:
        for line in problems:
            print(line, file=sys.stderr)
        return 1

    if args.expect and len(cases) != args.expect:
        print("parsed %d cases, expected %d" % (len(cases), args.expect), file=sys.stderr)
        return 1

    write_jsonl(args.out, cases)
    print("wrote %d cases to %s" % (len(cases), args.out))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
