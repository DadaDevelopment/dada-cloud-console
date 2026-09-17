#!/usr/bin/env python3
"""Register the models our agents run on in a Langfuse project so every
generation gets a cost.

Langfuse prices a generation only when the model name on the observation
matches a model definition. It ships definitions for OpenAI, Anthropic and
Google, but nothing for Zhipu's GLM family, which is what the Telegram agents
run on, so their traces show tokens and no money. This script reads
models.json (prices per 1M tokens, source noted inside) and creates, through
POST /api/public/models, one project-scoped definition per model that the
project does not already have. It never edits or deletes: a changed price is
a new row with a start date, which keeps old traces priced as they were.

Match pattern is the exact name, case-insensitive, so `glm-5.3-flash` prices
`GLM-5.3-Flash` but not `glm-5.3`. Usage keys are the ones the kagent patch
emits (input/output) plus OpenAI-style input_cached_tokens.

    export LANGFUSE_PUBLIC_KEY=... LANGFUSE_SECRET_KEY=...
    python3 scripts/langfuse/ensure_models.py
    python3 scripts/langfuse/ensure_models.py --dry-run
"""

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request
from base64 import b64encode
from pathlib import Path

DEFAULT_HOST = "https://cloud.langfuse.com"
MODELS_PATH = "/api/public/models"
PER_MILLION = 1_000_000


def eprint(*args):
    print(*args, file=sys.stderr)


def credentials():
    public = os.environ.get("LANGFUSE_PUBLIC_KEY", "").strip()
    secret = os.environ.get("LANGFUSE_SECRET_KEY", "").strip()
    if not public or not secret:
        eprint("LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY are not set (use the keys of the project whose traces need prices).")
        raise SystemExit(1)
    host = os.environ.get("LANGFUSE_HOST", "").strip() or os.environ.get("LANGFUSE_BASE_URL", "").strip() or DEFAULT_HOST
    return host.rstrip("/"), public, secret


def request(host, public, secret, method, path, body=None, timeout=30):
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(host + path, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Basic " + b64encode(("%s:%s" % (public, secret)).encode("utf-8")).decode("ascii"))
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        raise RuntimeError("%s %s: HTTP %d: %s" % (method, path, exc.code, exc.read().decode("utf-8", "replace")[:400]))
    return json.loads(raw) if raw else {}


def existing_models(host, public, secret):
    """Model definitions the project already has, keyed by lower-cased name.

    Only the project's own rows count: a Langfuse-managed definition cannot
    be relied on to exist tomorrow and cannot carry our price.
    """
    out = {}
    page = 1
    while True:
        resp = request(host, public, secret, "GET", "%s?page=%d&limit=100" % (MODELS_PATH, page))
        for model in resp.get("data") or []:
            if model.get("isLangfuseManaged"):
                continue
            out[str(model.get("modelName", "")).lower()] = model
        meta = resp.get("meta") or {}
        if page >= int(meta.get("totalPages") or 1):
            return out
        page += 1


def definition(model):
    prices = {
        "input": model["input"] / PER_MILLION,
        "output": model["output"] / PER_MILLION,
    }
    if model.get("cache_read") is not None:
        prices["input_cached_tokens"] = model["cache_read"] / PER_MILLION
    return {
        "modelName": model["name"],
        "matchPattern": "(?i)^(%s)$" % re.escape(model["name"]),
        "unit": "TOKENS",
        "pricingTiers": [
            {
                "id": "default",
                "name": "Standard",
                "isDefault": True,
                "priority": 0,
                "conditions": [],
                "prices": prices,
            }
        ],
    }


def main():
    parser = argparse.ArgumentParser(description="Create missing model price definitions in a Langfuse project.")
    parser.add_argument("--models", default=str(Path(__file__).resolve().parent / "models.json"))
    parser.add_argument("--dry-run", action="store_true", help="print what would be created, send nothing")
    args = parser.parse_args()

    with open(args.models, encoding="utf-8") as fh:
        wanted = json.load(fh)["models"]

    host, public, secret = credentials()
    have = existing_models(host, public, secret)
    created = 0
    for model in wanted:
        if model["name"].lower() in have:
            print("have    %s" % model["name"])
            continue
        body = definition(model)
        if args.dry_run:
            print("create  %s  in=%s out=%s /1M" % (model["name"], model["input"], model["output"]))
            continue
        request(host, public, secret, "POST", MODELS_PATH, body)
        created += 1
        print("created %s  in=%s out=%s /1M" % (model["name"], model["input"], model["output"]))
    print("%d model(s) created in %s" % (created, host))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
