# kagent-app

Dada build of the kagent Python agent runtime (`ghcr.io/kagent-dev/kagent/app`)
with one build-time patch: `patch_tracing.py` (see its docstring) plus the
`kagent/core/tracing/_dada.py` module it installs. Everything else is upstream
byte for byte.

Published by Jenkins as `ghcr.io/dadadevelopment/dada-cloud-kagent-app:<sha>`
and selected cluster-wide in argo-infra
`clusters/beget-prod/projects/agents/environments/prod/apps/kagent/values.yaml`
under `controller.agentImage`. Every python Declarative agent restarts on a
tag bump.

The upstream image is distroless (no shell, runtime user `65532`, site-packages
owned by root), so the Dockerfile patches in a throwaway stage under the
image's own venv python and copies the `kagent/core/tracing/` package back over
a clean upstream layer together with the patched `kagent/adk/_agent_executor.py`.
User, entrypoint and env stay upstream.

Upgrading kagent: bump `KAGENT_VERSION` in the Dockerfile together with the
chart `targetRevision`; the patch asserts its anchors and fails the build if
upstream moved the tracing code, and the final `COPY --from` fails if the
python minor (`python3.13` in the site-packages path) changed.

Local check:

```bash
docker build -t kagent-app-test kagent-app
docker run --rm --entrypoint /.kagent/.venv/bin/python kagent-app-test -c \
  'import kagent.core.tracing._utils as m; s=open(m.__file__).read(); assert "KAGENT_INSTRUMENT_HTTPX" in s and "exclude_spans" in s; print("ok")'
```

Runtime env honoured by the patch:

| env | default | effect |
|---|---|---|
| `KAGENT_INSTRUMENT_HTTPX` | `false` | `true` restores upstream httpx client spans |
| `KAGENT_INSTRUMENT_OPENAI` | `false` | `true` restores the `openai.chat` span (duplicate of ADK `generate_content`) |
| `LANGFUSE_INGESTION_VERSION` | `4` when the OTLP endpoint host contains `langfuse` | value of the `x-langfuse-ingestion-version` header added to the trace exporter |
| `LANGFUSE_PUBLIC_KEY` / `LANGFUSE_SECRET_KEY` (else basic auth from `OTEL_EXPORTER_OTLP_HEADERS`), `LANGFUSE_HOST` | cloud.langfuse.com | at boot the pod publishes its system prompt to Langfuse as text prompt `<KAGENT_NAME>` (label `production`, commit message `PROMPT_VERSION`, no new version when the text is unchanged) and links every span to it via `langfuse.observation.prompt.*`; rides the gitops rollout, no console call involved |
| `LANGFUSE_TRACING_ENVIRONMENT` | `default` | `langfuse.environment` |
| `PROMPT_VERSION` | unset | `langfuse.release` + `trace.metadata.prompt_version` |

Every A2A turn also maps `message.metadata` keys `dada.*` (channel, chat_id,
user_id, username, first_name, thread_id, trigger) to Langfuse: user id is
`@username` (else `<channel>:<user_id>`), session id is the user id plus
`@<chat_id>` for group chats and `#<thread_id>` for forum threads, trace name is
`<agent> <channel> <trigger>`. The FastAPI root span is renamed the same way and
gets `observation.type=agent`, the user text as input, the final task text as
output, the task state, token usage and ERROR level on failure. ADK's own
`invocation` and `invoke_agent` spans get the same user text as input when they
start and the latest answer text as output while the executor drains events, so
no observation in the tree is blank. For `generate_content` input/output the cluster also sets
`OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental` and
`OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=SPAN_ONLY` (argo-infra
composition `baselineEnv`); without them ADK sends message content to OTel logs.
