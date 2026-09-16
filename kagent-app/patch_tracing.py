"""Trim kagent runtime tracing to spans that mean something in Langfuse.

Upstream kagent-core wires HTTPXClientInstrumentor unconditionally and lets the
FastAPI instrumentor emit the ASGI `http send` / `http receive` sub-spans. On a
Declarative agent that is ~26 of ~38 spans per turn: kagent-controller session
bookkeeping, MCP handshakes, ASGI internals. Neither instrumentor reads an env
kill switch in the versions pinned by kagent 0.10 (opentelemetry-instrumentation
0.59b0 only honours OTEL_PYTHON_DISABLED_INSTRUMENTATIONS under the
`opentelemetry-instrument` distro, and httpx 0.59b0 has no excluded_urls at all),
so the image patches the source at build time.

After the patch:
  * httpx client spans are emitted only when KAGENT_INSTRUMENT_HTTPX=true;
  * FastAPI keeps the request span but drops the send/receive children.

Every anchor is asserted so an upstream bump that moves the code fails the
build instead of silently shipping the noisy runtime again.

The upstream image is distroless (no shell, no rm) and ships only
``*.opt-2.pyc`` next to the sources, so the script runs under the image's own
venv python as root in a throwaway build stage and regenerates that pyc; the
final stage copies the whole ``tracing/`` package over a clean upstream layer.
"""

import importlib.util
import pathlib
import py_compile
import sys

ANCHOR_HTTPX = "        HTTPXClientInstrumentor().instrument()\n"
PATCHED_HTTPX = (
    '        if os.getenv("KAGENT_INSTRUMENT_HTTPX", "false").lower() == "true":\n'
    "            HTTPXClientInstrumentor().instrument()\n"
)
ANCHOR_FASTAPI = (
    "            FastAPIInstrumentor().instrument_app(fastapi_app, excluded_urls=_excluded_urls)\n"
)
PATCHED_FASTAPI = (
    "            FastAPIInstrumentor().instrument_app(\n"
    '                fastapi_app, excluded_urls=_excluded_urls, exclude_spans=["send", "receive"]\n'
    "            )\n"
)


def locate() -> pathlib.Path:
    spec = importlib.util.find_spec("kagent.core.tracing._utils")
    if spec is None or spec.origin is None:
        sys.exit("kagent.core.tracing._utils not importable in this image")
    return pathlib.Path(spec.origin)


def replace_once(text: str, anchor: str, replacement: str) -> str:
    count = text.count(anchor)
    if count != 1:
        sys.exit(f"anchor found {count} times, expected exactly 1:\n{anchor}")
    return text.replace(anchor, replacement)


def main() -> None:
    path = locate()
    original = path.read_text()
    patched = replace_once(original, ANCHOR_HTTPX, PATCHED_HTTPX)
    patched = replace_once(patched, ANCHOR_FASTAPI, PATCHED_FASTAPI)
    if "\nimport os\n" not in patched:
        sys.exit("expected `import os` in _utils.py")
    path.write_text(patched)
    pyc = py_compile.compile(str(path), optimize=2, doraise=True)
    print(f"patched {path}\nrecompiled {pyc}")


if __name__ == "__main__":
    main()
