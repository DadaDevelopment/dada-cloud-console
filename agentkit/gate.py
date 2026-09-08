"""Pre-merge gate for an agent package: golden set, prompt hygiene, skill tree.

Model-free by design. A gate that needs an API key does not run on every
commit: it gets a credential, then a timeout, then a ``|| true``. Everything
here is stdlib and finishes in milliseconds, so it can sit in the deploy lane
and actually block a merge.

What it does NOT do is score the agent. Scoring needs a model and lives in the
bakeoff, run on the holdout split before a release. This gate only guarantees
that the material the bakeoff consumes is well-formed and that the prompt
itself is free of the tells the agent is told to avoid.

Usage:
    python3 gate.py --agents-root <dir> --agent <name> --cases <file.jsonl>
"""

import argparse
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import evalspec
import humanize
import skills

PROMPT_RULES = ("forbidden_unicode", "em_dash", "banned_phrase")

MIN_CASES = 12
MIN_HOLDOUT = 6
MIN_SILENCE_SHARE = 0.2

QUOTED = re.compile(r"[«\u201c][^«»\u201c\u201d]*[»\u201d]")

SILENCE_WORD = re.compile(r"молч", re.IGNORECASE)
ALTERNATIVE_WORD = re.compile(r"\bлибо\b|\bили\b", re.IGNORECASE)


def unquote(text: str) -> str:
    """Blank out quoted spans before linting a prompt.

    A prompt earns the right to name a banned phrase: the line that forbids
    "отличный вопрос" has to spell it out. Quoted material is the agent's
    vocabulary list, not the agent's voice, so it is exempt. Everything
    outside the quotes is the voice and stays under the rules.
    """
    return QUOTED.sub(" ", text)


def check_cases(path: str) -> list[str]:
    """Golden set is loadable, split both ways, and not all-reply."""
    problems: list[str] = []
    try:
        cases = evalspec.load_cases(path)
    except (evalspec.CaseError, OSError) as exc:
        return [f"golden set unreadable: {exc}"]

    dev = [c for c in cases if c["split"] == "dev"]
    holdout = [c for c in cases if c["split"] == "holdout"]
    if len(cases) < MIN_CASES:
        problems.append(f"only {len(cases)} cases, need at least {MIN_CASES}")
    if not dev:
        problems.append("dev split is empty: nothing to tune on")
    if len(holdout) < MIN_HOLDOUT:
        problems.append(f"holdout split has {len(holdout)} cases, need at least {MIN_HOLDOUT}")

    for case in cases:
        expect = case["expect"]
        rubric = expect.get("rubric", "")
        offers_a_choice = SILENCE_WORD.search(rubric) and ALTERNATIVE_WORD.search(rubric)
        if offers_a_choice and not (expect.get("allow_silence") or expect.get("allow_reply")):
            problems.append(
                f"case {case['id']}: rubric offers a choice between replying and silence "
                f"({rubric!r}) but neither allow_silence nor allow_reply is set: the model "
                "will fail a case its own rubric says it passed"
            )

    silent = [c for c in cases if not c["expect"]["should_reply"]]
    share = len(silent) / len(cases) if cases else 0
    if share < MIN_SILENCE_SHARE:
        problems.append(
            f"only {len(silent)}/{len(cases)} cases expect silence "
            f"({share:.0%} < {MIN_SILENCE_SHARE:.0%}): a set that always wants an answer "
            "cannot catch an agent that never shuts up"
        )
    return problems


def check_prompt(base_path: str, agent: str) -> list[str]:
    """The prompt must obey the style it preaches."""
    problems: list[str] = []
    try:
        skill_set = skills.SkillSet(base_path, agent)
        texts = {"core.md": unquote(skill_set.core())}
        for domain in skill_set.domains():
            texts[f"domains/{domain}.md"] = unquote(skill_set.load(domain))
    except skills.SkillError as exc:
        return [f"skill tree unreadable: {exc}"]

    for name, text in texts.items():
        for finding in humanize.inspect(text, max_chars=10 ** 6, max_sentences=10 ** 6):
            if finding.rule in PROMPT_RULES:
                problems.append(f"{name}: {finding.rule}: {finding.detail}")
    return problems


def check_skill_tree(base_path: str, agent: str) -> list[str]:
    """Every domain file is reachable from core.md, and core stays readable."""
    try:
        audit = skills.SkillSet(base_path, agent).audit()
    except skills.SkillError as exc:
        return [f"skill tree unreadable: {exc}"]

    problems: list[str] = []
    for domain in audit["unreferenced_domains"]:
        problems.append(f"domain {domain!r} is never named in core.md: dead file or dead lever")
    if not audit["domains"]:
        problems.append("agent has no domain files: the whole prompt lives in core.md")
    return problems


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--agents-root", required=True, help="directory holding agents/<name>/")
    ap.add_argument("--agent", required=True)
    ap.add_argument("--cases", required=True)
    args = ap.parse_args()

    sections = [
        ("golden set", check_cases(args.cases)),
        ("prompt hygiene", check_prompt(args.agents_root, args.agent)),
        ("skill tree", check_skill_tree(args.agents_root, args.agent)),
    ]

    failed = False
    for title, problems in sections:
        if problems:
            failed = True
            print(f"FAIL {title}:")
            for p in problems:
                print(f"  - {p}")
        else:
            print(f"ok   {title}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
