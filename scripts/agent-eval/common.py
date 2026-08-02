"""Shared primitives for the console agent eval harness.

Standard library only. Holds the tool inventory mirrored from
``backend/internal/agentchat/toolset.go``, an SSE reader that matches the
framing the console panel itself consumes, and thin HTTP helpers.
"""

import json
import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Iterator, NamedTuple, Optional

BASELINE_READ_TOOLS = [
    "listProjects",
    "getProject",
    "listApps",
    "getAppState",
    "getAppLogs",
    "getAppMetrics",
    "listDeployments",
    "listBuilds",
    "getBuild",
    "listEnvVars",
    "listHostnames",
    "listEndpoints",
    "listDatabases",
    "listOperations",
    "getOperation",
    "searchLogs",
    "getProjectCost",
    "getCurrentUser",
    "downloadSourceArchive",
    "create_support_ticket",
    "deleteAppImpact",
    "deleteProjectImpact",
    "moveAppImpact",
]

BASELINE_WRITE_TOOLS = [
    "restartApp",
    "triggerBuild",
    "deployTrigger",
    "cancelBuild",
    "retryOperation",
    "setEnvVar",
    "deleteEnvVar",
    "rollbackApp",
    "rollbackDeployment",
    "promoteDeployment",
    "updateAppImage",
    "updateAppProfile",
    "updateAppStorage",
    "createDatabase",
    "createEndpoint",
    "createS3Bucket",
]

BASELINE_DENY_TOOLS = [
    "revealEnvVar",
    "getDatabaseCredentials",
    "getS3BucketCredentials",
    "revealModelApiKey",
]

BASELINE_NOT_A_TOOL_FEATURES = [
    "createApp",
    "createProject",
    "connectGitRepo",
    "listGitInstallations",
    "getGitInstallUrl",
    "addDomainAuthorization",
    "verifyDomainAuthorization",
    "boxUp",
    "createBox",
    "crystallizeBox",
    "createAppServer",
    "deleteApp",
    "deleteProject",
    "moveApp",
]

TOOL_RENAMES = {"submitFeedback": "create_support_ticket"}

DEFAULT_API_BASE = "https://console.dada-tuda.ru/api/v1"


def repo_root() -> Path:
    """Repository root, derived from this file's location."""
    return Path(__file__).resolve().parents[2]


def _go_list_block(src: str, header: str) -> list:
    start = src.find(header)
    if start < 0:
        return []
    end = src.find("}", start)
    if end < 0:
        return []
    body = src[start + len(header) : end]
    body = re.sub(r"//[^\n]*", "", body)
    return re.findall(r'"([^"]+)"', body)


def read_tool_inventory(root: Optional[Path] = None):
    """Parse the live tool allowlist out of toolset.go.

    Returns ``(read_tools, write_tools, deny_tools, problems)``. The allowlist
    is deliberately read from source rather than hardcoded: it is the exact
    thing the benchmark exists to watch, and a stale copy here would silently
    mis-classify a write tool as a read one.
    """
    root = root or repo_root()
    path = root / "backend" / "internal" / "agentchat" / "toolset.go"
    try:
        src = path.read_text(encoding="utf-8")
    except OSError as exc:
        return list(BASELINE_READ_TOOLS), list(BASELINE_WRITE_TOOLS), list(BASELINE_DENY_TOOLS), [
            "cannot read %s: %s" % (path, exc)
        ]

    keep = [TOOL_RENAMES.get(n, n) for n in _go_list_block(src, "var keepTools = []string{")]
    writes = _go_list_block(src, "var writeKeepTools = []string{")
    deny = _go_list_block(src, "var denyTools = map[string]bool{")

    problems = []
    for label, parsed in (("keepTools", keep), ("writeKeepTools", writes), ("denyTools", deny)):
        if not parsed:
            problems.append("%s: could not parse the block out of %s" % (label, path))

    if problems:
        return list(BASELINE_READ_TOOLS), list(BASELINE_WRITE_TOOLS), list(BASELINE_DENY_TOOLS), problems
    return keep, writes, deny, []


def inventory_drift(read_tools, write_tools, deny_tools) -> list:
    """Describe how the live allowlist differs from the spec's baseline.

    Drift is information, not an error: it means the spec's fact sheet and the
    per-case expectations may need a re-read, which a human has to decide.
    """
    notes = []
    for label, actual, baseline in (
        ("keepTools", read_tools, BASELINE_READ_TOOLS),
        ("writeKeepTools", write_tools, BASELINE_WRITE_TOOLS),
        ("denyTools", deny_tools, BASELINE_DENY_TOOLS),
    ):
        added = [n for n in actual if n not in baseline]
        removed = [n for n in baseline if n not in actual]
        if added:
            notes.append("%s: new since the spec was written: %s" % (label, ", ".join(added)))
        if removed:
            notes.append("%s: gone since the spec was written: %s" % (label, ", ".join(removed)))
    return notes


READ_TOOLS, WRITE_TOOLS, DENY_TOOLS, INVENTORY_PROBLEMS = read_tool_inventory()
ALL_TOOLS = READ_TOOLS + WRITE_TOOLS
NOT_A_TOOL_FEATURES = [n for n in BASELINE_NOT_A_TOOL_FEATURES if n not in ALL_TOOLS]


class SSEEvent(NamedTuple):
    """One decoded server-sent event."""

    event: str
    data: str


def iter_sse(resp) -> Iterator[SSEEvent]:
    """Decode an SSE byte stream the same way the console panel does.

    Leading whitespace after ``data:`` is preserved because the backend emits
    frames without the cosmetic space and the panel does not strip one either;
    stripping here would eat real token text.
    """
    current = "message"
    data_lines: list = []

    while True:
        raw = resp.readline()
        if not raw:
            break
        line = raw.decode("utf-8", errors="replace").rstrip("\n").rstrip("\r")
        if line == "":
            data = "\n".join(data_lines)
            event = current
            current = "message"
            data_lines = []
            if data == "" and event == "message":
                continue
            yield SSEEvent(event=event, data=data)
            continue
        if line.startswith(":"):
            continue
        if line.startswith("event:"):
            current = line[len("event:") :].strip()
            continue
        if line.startswith("data:"):
            data_lines.append(line[len("data:") :])

    if data_lines:
        yield SSEEvent(event=current, data="\n".join(data_lines))


def api_base() -> str:
    """Console API base URL from DADA_API, without a trailing slash."""
    return os.environ.get("DADA_API", DEFAULT_API_BASE).rstrip("/")


def bearer() -> str:
    """Console bearer token from DADA_BEARER, or exit with an explanation."""
    token = os.environ.get("DADA_BEARER", "").strip()
    if not token:
        raise SystemExit(
            "DADA_BEARER is not set. Export a console access token first:\n"
            "  export DADA_BEARER=$(pbpaste)   # token from the console dev tools"
        )
    return token


def _request(method: str, url: str, token: str, body=None, accept: str = "application/json"):
    payload = None
    if body is not None:
        payload = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=payload, method=method)
    req.add_header("Accept", accept)
    req.add_header("Authorization", "Bearer %s" % token)
    if payload is not None:
        req.add_header("Content-Type", "application/json")
    return req


def http_json(method: str, url: str, token: str, body=None, timeout: int = 30):
    """Perform a JSON request, returning (status, decoded-body-or-raw-text)."""
    req = _request(method, url, token, body)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            status = resp.getcode()
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        status = exc.code
    try:
        return status, json.loads(raw) if raw else {}
    except ValueError:
        return status, {"raw": raw}


def http_sse(url: str, token: str, body, timeout: int = 180):
    """Open an SSE POST stream; the caller feeds the response to iter_sse."""
    req = _request("POST", url, token, body, accept="text/event-stream")
    return urllib.request.urlopen(req, timeout=timeout)


def extract_json_object(text: str):
    """Pull the first balanced JSON object out of a model reply."""
    if not text:
        return None
    depth = 0
    start = -1
    in_string = False
    escaped = False
    for idx, ch in enumerate(text):
        if in_string:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
            continue
        if ch == '"':
            in_string = True
            continue
        if ch == "{":
            if depth == 0:
                start = idx
            depth += 1
            continue
        if ch == "}":
            if depth > 0:
                depth -= 1
                if depth == 0 and start >= 0:
                    try:
                        return json.loads(text[start : idx + 1])
                    except ValueError:
                        start = -1
    return None


def load_jsonl(path) -> list:
    """Read a JSONL file into a list of dicts, skipping blank lines."""
    out = []
    with open(path, "r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line:
                out.append(json.loads(line))
    return out


def write_jsonl(path, rows) -> None:
    """Write rows as JSONL, creating parent directories as needed."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        for row in rows:
            fh.write(json.dumps(row, ensure_ascii=False) + "\n")


def eprint(*args) -> None:
    """Print to stderr."""
    print(*args, file=sys.stderr)
