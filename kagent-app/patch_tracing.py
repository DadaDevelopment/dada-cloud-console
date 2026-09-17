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
  * FastAPI keeps the request span but drops the send/receive children;
  * the OpenAI client instrumentor (a third copy of every LLM call next to
    ADK's call_llm/generate_content) runs only when KAGENT_INSTRUMENT_OPENAI=true;
  * the OTLP trace exporter sends ``x-langfuse-ingestion-version: 4`` when the
    endpoint is Langfuse (see ``_dada.exporter_headers``);
  * the A2A executor shapes each turn for Langfuse v4 (user/session from the
    caller's ``dada.*`` message metadata, readable root name, root
    input/output, prompt link, release) through ``kagent.core.tracing._dada``;
  * the ``static`` CLI command hands the system prompt it booted with to
    ``_dada.register_prompt`` so the pod publishes it to Langfuse once per
    rollout and every span links to that prompt version.

Every anchor is asserted so an upstream bump that moves the code fails the
build instead of silently shipping the noisy runtime again.

``_dada.py`` is copied next to this script and installed into the tracing
package before the executor is patched to import it.

The upstream image is distroless (no shell, no rm) and ships only
``*.opt-2.pyc`` next to the sources, so the script runs under the image's own
venv python as root in a throwaway build stage and regenerates that pyc; the
final stage copies the whole ``tracing/`` package over a clean upstream layer.
"""

import importlib
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
ANCHOR_OPENAI = "        if instrument_openai_client:\n            OpenAIInstrumentor().instrument()\n"
PATCHED_OPENAI = (
    '        if instrument_openai_client and os.getenv("KAGENT_INSTRUMENT_OPENAI", "false").lower() == "true":\n'
    "            OpenAIInstrumentor().instrument()\n"
)
ANCHOR_EXPORTER = (
    "            processor = BatchSpanProcessor(\n"
    "                _create_span_exporter(endpoint=trace_endpoint, timeout=trace_timeout_seconds)\n"
    "            )\n"
)
PATCHED_EXPORTER = (
    "            from kagent.core.tracing._dada import exporter_headers\n"
    "\n"
    "            processor = BatchSpanProcessor(\n"
    "                _create_span_exporter(\n"
    "                    endpoint=trace_endpoint, timeout=trace_timeout_seconds, headers=exporter_headers(trace_endpoint)\n"
    "                )\n"
    "            )\n"
)

ANCHOR_CLI_IMPORT = "from kagent.core import KAgentConfig, configure_logging, configure_tracing\n"
PATCHED_CLI_IMPORT = (
    "from kagent.core import KAgentConfig, configure_logging, configure_tracing\n"
    "from kagent.core.tracing import _dada as dada_tracing\n"
)
ANCHOR_CLI_PROMPT = (
    "    agent_config = AgentConfig.model_validate(config)\n"
    '    with open(os.path.join(filepath, "agent-card.json"), "r") as f:\n'
)
PATCHED_CLI_PROMPT = (
    "    agent_config = AgentConfig.model_validate(config)\n"
    "    dada_tracing.register_prompt(agent_config.instruction)\n"
    '    with open(os.path.join(filepath, "agent-card.json"), "r") as f:\n'
)

ANCHOR_EXEC_IMPORT = "from kagent.core.tracing._span_processor import (\n"
PATCHED_EXEC_IMPORT = (
    "from kagent.core.tracing import _dada as dada_tracing\n"
    "from kagent.core.tracing._span_processor import (\n"
)
ANCHOR_EXEC_BEGIN = "        context_token = set_kagent_span_attributes(span_attributes)\n"
PATCHED_EXEC_BEGIN = (
    "        dada_tracing.begin_turn(context, run_args, span_attributes)\n"
    "        context_token = set_kagent_span_attributes(span_attributes)\n"
)
ANCHOR_EXEC_END = "        # publish the task result event - this is final\n"
PATCHED_EXEC_END = (
    "        dada_tracing.end_turn(\n"
    "            task_result_aggregator.task_state, task_result_aggregator.task_status_message, run_metadata\n"
    "        )\n"
    "        # publish the task result event - this is final\n"
)
ANCHOR_EXEC_FAIL = (
    "        error_message: str,\n"
    "    ) -> None:\n"
    "        try:\n"
    "            await event_queue.enqueue_event(\n"
    "                TaskStatusUpdateEvent(\n"
    "                    task_id=context.task_id,\n"
    "                    status=TaskStatus(\n"
    "                        state=TaskState.failed,\n"
)
PATCHED_EXEC_FAIL = (
    "        error_message: str,\n"
    "    ) -> None:\n"
    "        dada_tracing.fail_turn(error_message)\n"
    "        try:\n"
    "            await event_queue.enqueue_event(\n"
    "                TaskStatusUpdateEvent(\n"
    "                    task_id=context.task_id,\n"
    "                    status=TaskStatus(\n"
    "                        state=TaskState.failed,\n"
)


def locate(module: str) -> pathlib.Path:
    spec = importlib.util.find_spec(module)
    if spec is None or spec.origin is None:
        sys.exit(f"{module} not importable in this image")
    return pathlib.Path(spec.origin)


def replace_once(text: str, anchor: str, replacement: str) -> str:
    count = text.count(anchor)
    if count != 1:
        sys.exit(f"anchor found {count} times, expected exactly 1:\n{anchor}")
    return text.replace(anchor, replacement)


def patch_file(path: pathlib.Path, edits: list[tuple[str, str]]) -> None:
    patched = path.read_text()
    for anchor, replacement in edits:
        patched = replace_once(patched, anchor, replacement)
    path.write_text(patched)
    pyc = py_compile.compile(str(path), optimize=2, doraise=True)
    print(f"patched {path}\nrecompiled {pyc}")


def install_dada_module(tracing_dir: pathlib.Path) -> None:
    source = pathlib.Path(__file__).with_name("_dada.py")
    target = tracing_dir / "_dada.py"
    target.write_text(source.read_text())
    pyc = py_compile.compile(str(target), optimize=2, doraise=True)
    print(f"installed {target}\ncompiled {pyc}")


def main() -> None:
    utils = locate("kagent.core.tracing._utils")
    if "\nimport os\n" not in utils.read_text():
        sys.exit("expected `import os` in _utils.py")
    install_dada_module(utils.parent)
    patch_file(
        utils,
        [
            (ANCHOR_HTTPX, PATCHED_HTTPX),
            (ANCHOR_FASTAPI, PATCHED_FASTAPI),
            (ANCHOR_OPENAI, PATCHED_OPENAI),
            (ANCHOR_EXPORTER, PATCHED_EXPORTER),
        ],
    )
    patch_file(
        locate("kagent.adk._agent_executor"),
        [
            (ANCHOR_EXEC_IMPORT, PATCHED_EXEC_IMPORT),
            (ANCHOR_EXEC_BEGIN, PATCHED_EXEC_BEGIN),
            (ANCHOR_EXEC_END, PATCHED_EXEC_END),
            (ANCHOR_EXEC_FAIL, PATCHED_EXEC_FAIL),
        ],
    )
    patch_file(
        locate("kagent.adk.cli"),
        [
            (ANCHOR_CLI_IMPORT, PATCHED_CLI_IMPORT),
            (ANCHOR_CLI_PROMPT, PATCHED_CLI_PROMPT),
        ],
    )
    importlib.import_module("kagent.core.tracing._dada")


if __name__ == "__main__":
    main()
