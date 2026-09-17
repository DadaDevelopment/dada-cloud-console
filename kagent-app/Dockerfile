ARG KAGENT_VERSION=0.10.0-rc3

FROM ghcr.io/kagent-dev/kagent/app:${KAGENT_VERSION} AS patch
USER 0
COPY patch_tracing.py _dada.py /
RUN ["/.kagent/.venv/bin/python", "/patch_tracing.py"]

FROM ghcr.io/kagent-dev/kagent/app:${KAGENT_VERSION}
COPY --from=patch /.kagent/.venv/lib/python3.13/site-packages/kagent/core/tracing/ /.kagent/.venv/lib/python3.13/site-packages/kagent/core/tracing/
COPY --from=patch /.kagent/.venv/lib/python3.13/site-packages/kagent/adk/_agent_executor.py /.kagent/.venv/lib/python3.13/site-packages/kagent/adk/_agent_executor.py
COPY --from=patch /.kagent/.venv/lib/python3.13/site-packages/kagent/adk/__pycache__/_agent_executor.cpython-313.opt-2.pyc /.kagent/.venv/lib/python3.13/site-packages/kagent/adk/__pycache__/_agent_executor.cpython-313.opt-2.pyc
