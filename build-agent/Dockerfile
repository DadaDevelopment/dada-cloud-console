# Binary is prebuilt by the CI go-builder stage (bin/build-agent, static
# linux/amd64). Do NOT recompile here: an in-container golang build pegs dind
# CPU/mem and drops the Jenkins agent channel mid-build (see gitops-agent
# Dockerfile, builds #138-#145). Mirror backend/gitops-agent — copy the
# prebuilt binary into a minimal runtime.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY bin/build-agent ./build-agent
EXPOSE 8091
ENTRYPOINT ["./build-agent"]
