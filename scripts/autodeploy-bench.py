#!/usr/bin/env python3
"""Run the autodeploy benchmark over tasks/autodeploy-corpus.yaml.

Four stages, per tasks/autodeploy-benchmark-50-oss.md:

1. detected  -- the production detector (backend/internal/sourcedetect via
   cmd/autodeploy-detect) names a framework AND the expected port;
2. built     -- ``docker build`` of the repo's own Dockerfile succeeds;
3. up        -- the container stays running for --settle seconds;
4. answers   -- the mapped port replies 2xx/3xx on the corpus probe path.

Stages 2-4 are only meaningful for repos that carry their own Dockerfile.
Everything else is templated by the Jenkins shared library
(``dadaBuildPipeline.renderDockerfile``), which lives outside this repository
and cannot be exercised here; those rows are reported as ``template_only``
rather than counted as failures, and the report must say so.

Failure reasons come from the fixed vocabulary in the benchmark doc; a reason
outside it is a bug in this script, not a new kind of failure.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

REASONS = {
    "no_manifest",
    "wrong_port",
    "build_oom",
    "missing_env",
    "needs_db",
    "needs_migration",
    "monorepo_root",
    "non_http",
    "build_failed",
    "not_running",
    "no_response",
    "template_only",
    "download_failed",
}

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load_corpus(path):
    """Parse the corpus.

    Its shape is a fixed two-level list of scalars, so a real YAML parser would
    only add a dependency the run does not need.
    """
    repos, cur = [], None
    for line in open(path, encoding="utf-8"):
        line = line.rstrip("\n")
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if line.startswith("  - repo:"):
            cur = {"repo": line.split(":", 1)[1].strip()}
            repos.append(cur)
            continue
        if line.startswith("    ") and cur is not None and ":" in line:
            key, val = line.strip().split(":", 1)
            val = val.strip()
            if val.startswith("[") and val.endswith("]"):
                val = [v.strip() for v in val[1:-1].split(",") if v.strip()]
            elif val.isdigit():
                val = int(val)
            cur[key] = val
    return repos


def download(repo, commit, dest):
    """Fetch the pinned tarball from codeload, which is reachable from this host
    while api.github.com is not."""
    url = f"https://codeload.github.com/{repo}/tar.gz/{commit}"
    req = urllib.request.Request(url, headers={"User-Agent": "dada-autodeploy-bench"})
    with urllib.request.urlopen(req, timeout=180) as resp, open(dest, "wb") as fh:
        shutil.copyfileobj(resp, fh)
    return os.path.getsize(dest)


def detect(binary, archive):
    """Run the production detector over one archive and return its JSON row."""
    out = subprocess.run([binary, archive], capture_output=True, text=True, timeout=300)
    if out.returncode != 0:
        return {"error": out.stderr.strip() or f"exit {out.returncode}"}
    return json.loads(out.stdout.strip().splitlines()[-1])


def run(cmd, timeout, cwd=None):
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, cwd=cwd)


def classify_build_failure(log):
    """Map a build log to the fixed failure vocabulary."""
    low = log.lower()
    if "signal: killed" in low or "out of memory" in low or "oom" in low:
        return "build_oom"
    return "build_failed"


def classify_run_failure(log):
    """Map a container log to the fixed failure vocabulary."""
    low = log.lower()
    for needle, reason in (
        ("connection refused", "needs_db"),
        ("could not connect to server", "needs_db"),
        ("no such table", "needs_migration"),
        ("migrat", "needs_migration"),
    ):
        if needle in low:
            return reason
    if re.search(r"(env|environment) variable", low) or "is required" in low:
        return "missing_env"
    return "not_running"


def probe(port, path, attempts, delay):
    """Poll the published port until it answers or the attempts run out.

    Any transport error means "not up yet" and is retried; a 4xx/5xx is a real
    answer from a live server but does not satisfy stage 4.
    """
    url = f"http://127.0.0.1:{port}{path}"
    last = None
    for _ in range(attempts):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "dada-bench"})
            with urllib.request.urlopen(req, timeout=10) as resp:
                return resp.status, None
        except urllib.error.HTTPError as exc:
            if 200 <= exc.code < 400:
                return exc.code, None
            last = f"http {exc.code}"
        except Exception as exc:
            last = str(exc)
        time.sleep(delay)
    return None, last


def bench_one(entry, args, workdir):
    """Take one corpus row through as many stages as it can reach."""
    repo = entry["repo"]
    row = {
        "repo": repo,
        "commit": entry["commit"],
        "stack": entry.get("stack"),
        "shape": entry.get("shape"),
        "expected_port": entry.get("port"),
        "stages": {},
    }
    archive = os.path.join(workdir, repo.replace("/", "_") + ".tar.gz")
    try:
        row["archive_bytes"] = download(repo, entry["commit"], archive)
    except Exception as exc:
        row["stages"]["detected"] = False
        row["reason"] = "download_failed"
        row["detail"] = str(exc)
        return row

    det = detect(args.detector, archive)
    row["detected_framework"] = det.get("framework") or ""
    row["detected_port"] = det.get("port") or 0
    named = bool(row["detected_framework"])
    right_port = row["detected_port"] == entry.get("port")
    row["stages"]["detected"] = named and right_port
    if not named:
        row["reason"] = (
            "monorepo_root" if entry.get("shape") == "monorepo-subdir" else "no_manifest"
        )
    elif not right_port:
        row["reason"] = "wrong_port"

    if named and row["detected_framework"] != "docker":
        row.setdefault("reason", "template_only")
        return row
    if not args.build:
        return row

    tag = "dadabench/" + repo.replace("/", "-").lower()
    src = os.path.join(workdir, "src_" + repo.replace("/", "_"))
    os.makedirs(src, exist_ok=True)
    run(["tar", "-xzf", archive, "-C", src, "--strip-components=1"], timeout=600)
    build = run(
        ["docker", "build", "--pull", "-t", tag, "."], timeout=args.build_timeout, cwd=src
    )
    row["stages"]["built"] = build.returncode == 0
    if build.returncode != 0:
        row["reason"] = classify_build_failure(build.stdout + build.stderr)
        row["detail"] = (build.stdout + build.stderr)[-800:]
        return row

    port = entry.get("port") or row["detected_port"] or 8080
    name = "dadabench-" + repo.replace("/", "-").lower()
    run(["docker", "rm", "-f", name], timeout=120)
    up = run(
        ["docker", "run", "-d", "--name", name, "-p", f"{args.host_port}:{port}", tag],
        timeout=300,
    )
    if up.returncode != 0:
        row["stages"]["up"] = False
        row["reason"] = classify_run_failure(up.stdout + up.stderr)
        row["detail"] = (up.stdout + up.stderr)[-800:]
        return row
    try:
        time.sleep(args.settle)
        state = run(
            ["docker", "inspect", "-f", "{{.State.Running}}", name], timeout=60
        ).stdout.strip()
        row["stages"]["up"] = state == "true"
        if state != "true":
            logs = run(["docker", "logs", "--tail", "80", name], timeout=60)
            row["reason"] = classify_run_failure(logs.stdout + logs.stderr)
            row["detail"] = (logs.stdout + logs.stderr)[-800:]
            return row

        status, err = probe(
            args.host_port, entry.get("probe", "/"), args.probe_attempts, 5
        )
        row["stages"]["answers"] = status is not None
        row["http_status"] = status
        if status is None:
            logs = run(["docker", "logs", "--tail", "80", name], timeout=60)
            row["reason"] = "no_response"
            row["detail"] = f"{err} | {(logs.stdout + logs.stderr)[-600:]}"
        else:
            row.pop("reason", None)
    finally:
        run(["docker", "rm", "-f", name], timeout=180)
        if args.prune:
            run(["docker", "rmi", "-f", tag], timeout=300)
        shutil.rmtree(src, ignore_errors=True)
        if os.path.exists(archive):
            os.remove(archive)
    return row


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--corpus", default=os.path.join(ROOT, "tasks", "autodeploy-corpus.yaml")
    )
    ap.add_argument(
        "--detector", default=os.path.join(ROOT, "backend", "autodeploy-detect")
    )
    ap.add_argument(
        "--out", default=os.path.join(ROOT, "tasks", "autodeploy-results.jsonl")
    )
    ap.add_argument("--only", default="", help="substring filter on repo name")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--build", action="store_true", help="run stages 2-4 (needs docker)")
    ap.add_argument("--build-timeout", type=int, default=2400)
    ap.add_argument("--settle", type=int, default=60)
    ap.add_argument("--probe-attempts", type=int, default=12)
    ap.add_argument("--host-port", type=int, default=18080)
    ap.add_argument("--prune", action="store_true", help="delete images after each repo")
    args = ap.parse_args()

    corpus = load_corpus(args.corpus)
    if args.only:
        corpus = [c for c in corpus if args.only in c["repo"]]
    if args.limit:
        corpus = corpus[: args.limit]

    workdir = tempfile.mkdtemp(prefix="autodeploy-bench-")
    done = 0
    with open(args.out, "a", encoding="utf-8") as fh:
        for entry in corpus:
            started = time.time()
            try:
                row = bench_one(entry, args, workdir)
            except subprocess.TimeoutExpired as exc:
                row = {
                    "repo": entry["repo"],
                    "stages": {},
                    "reason": "build_failed",
                    "detail": f"timeout: {exc}",
                }
            row["seconds"] = round(time.time() - started, 1)
            if row.get("reason") and row["reason"] not in REASONS:
                row["detail"] = f"UNKNOWN REASON {row['reason']}: {row.get('detail', '')}"
                row["reason"] = "build_failed"
            fh.write(json.dumps(row, ensure_ascii=False) + "\n")
            fh.flush()
            done += 1
            print(
                f"[{done}/{len(corpus)}] {row['repo']} "
                f"{row.get('detected_framework', '')}:{row.get('detected_port', '')} "
                f"{row['stages']} {row.get('reason', 'ok')} {row['seconds']}s",
                flush=True,
            )
    shutil.rmtree(workdir, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
