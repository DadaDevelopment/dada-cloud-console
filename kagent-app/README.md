# kagent-app

Dada build of the kagent Python agent runtime (`ghcr.io/kagent-dev/kagent/app`)
with one build-time patch: `patch_tracing.py` (see its docstring). Everything
else is upstream byte for byte.

Published by Jenkins as `ghcr.io/dadadevelopment/dada-cloud-kagent-app:<sha>`
and selected cluster-wide in argo-infra
`clusters/beget-prod/projects/agents/environments/prod/apps/kagent/values.yaml`
under `controller.agentImage`. Every python Declarative agent restarts on a
tag bump.

The upstream image is distroless (no shell, runtime user `65532`, site-packages
owned by root), so the Dockerfile patches in a throwaway stage under the
image's own venv python and copies the `kagent/core/tracing/` package back over
a clean upstream layer. User, entrypoint and env stay upstream.

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
