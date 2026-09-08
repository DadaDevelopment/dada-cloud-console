"""Repository-level contract for an agent package, and its checker.

``gate.py`` knows how to check one agent when somebody hands it three paths.
That is fine for our own Jenkinsfile and useless for a client repository: the
paths live in our CI script, so the contract lives in our CI script, and a
client cannot satisfy a contract they cannot read.

So the paths move into the repository as data. One manifest at
``.dada/agent.json`` declares every agent the repo ships, and the checker takes
no arguments beyond the repo root. Same file is what the platform build lane
would look for.

JSON, not YAML, for the same reason the gate is model-free: the checker must
run with nothing but a python interpreter. A dependency is a thing that fails
on the day the check matters.

Manifest shape::

    {
      "version": 1,
      "agents": [
        {
          "name": "vibecoder",
          "agents_root": "tg-vibecoder",
          "cases": "tg-vibecoder/evals/persona/cases.jsonl",
          "holdout_threshold": 0.75
        }
      ]
    }

``holdout_threshold`` is required and is the point of the file. A threshold
that lives in someone's head is chosen after the run, which makes the holdout
split decorative.

Usage:
    python3 repospec.py --repo <dir>
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import gate

MANIFEST_PATH = os.path.join(".dada", "agent.json")

REQUIRED_KEYS = ("name", "agents_root", "cases", "holdout_threshold")

MIN_THRESHOLD = 0.5


class ManifestError(Exception):
    """Manifest is missing, unparseable, or does not describe an agent."""


def manifest_path(repo: str) -> str:
    return os.path.join(repo, MANIFEST_PATH)


def load_manifest(repo: str) -> list[dict]:
    """Read and validate the manifest, returning its agent entries.

    Validation is structural only: whether the declared paths hold a usable
    golden set and a usable skill tree is the gate's question, asked next.
    """
    path = manifest_path(repo)
    try:
        with open(path, encoding="utf-8") as handle:
            data = json.load(handle)
    except FileNotFoundError:
        raise ManifestError(f"no manifest at {MANIFEST_PATH}: the repo declares no agent")
    except (OSError, json.JSONDecodeError) as exc:
        raise ManifestError(f"{MANIFEST_PATH} unreadable: {exc}")

    if not isinstance(data, dict):
        raise ManifestError(f"{MANIFEST_PATH} must hold an object")
    if data.get("version") != 1:
        raise ManifestError(f"{MANIFEST_PATH}: unsupported version {data.get('version')!r}, expected 1")

    agents = data.get("agents")
    if not isinstance(agents, list) or not agents:
        raise ManifestError(f"{MANIFEST_PATH}: agents must be a non-empty list")

    seen: set[str] = set()
    for index, entry in enumerate(agents):
        if not isinstance(entry, dict):
            raise ManifestError(f"{MANIFEST_PATH}: agents[{index}] must be an object")
        missing = [k for k in REQUIRED_KEYS if k not in entry]
        if missing:
            raise ManifestError(f"{MANIFEST_PATH}: agents[{index}] missing {', '.join(missing)}")
        name = entry["name"]
        if name in seen:
            raise ManifestError(f"{MANIFEST_PATH}: duplicate agent {name!r}")
        seen.add(name)
        threshold = entry["holdout_threshold"]
        if not isinstance(threshold, (int, float)) or isinstance(threshold, bool):
            raise ManifestError(f"{MANIFEST_PATH}: {name}: holdout_threshold must be a number")
        if not MIN_THRESHOLD <= threshold <= 1:
            raise ManifestError(
                f"{MANIFEST_PATH}: {name}: holdout_threshold {threshold} outside [{MIN_THRESHOLD}, 1]"
            )
    return agents


def check_agent(repo: str, entry: dict) -> list[tuple[str, list[str]]]:
    """Run the model-free gate for one manifest entry."""
    root = os.path.join(repo, entry["agents_root"])
    cases = os.path.join(repo, entry["cases"])
    name = entry["name"]
    return [
        ("golden set", gate.check_cases(cases)),
        ("prompt hygiene", gate.check_prompt(root, name)),
        ("skill tree", gate.check_skill_tree(root, name)),
    ]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default=".", help="repository root holding .dada/agent.json")
    args = ap.parse_args()

    try:
        agents = load_manifest(args.repo)
    except ManifestError as exc:
        print(f"FAIL manifest:\n  - {exc}")
        return 1

    failed = False
    for entry in agents:
        print(f"agent {entry['name']} (holdout threshold {entry['holdout_threshold']})")
        for title, problems in check_agent(args.repo, entry):
            if problems:
                failed = True
                print(f"  FAIL {title}:")
                for problem in problems:
                    print(f"    - {problem}")
            else:
                print(f"  ok   {title}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
