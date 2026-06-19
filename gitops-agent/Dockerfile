# Binary is prebuilt by the CI go-builder stage (bin/gitops-agent, static
# linux/amd64). Do NOT recompile here: an in-container golang build pegs dind
# CPU/mem and drops the Jenkins agent channel mid-build (builds #138-#145).
# Mirror backend/Dockerfile — copy the prebuilt binary into a minimal runtime.
FROM alpine:3.19
RUN apk add --no-cache ca-certificates git openssh-client
WORKDIR /app
COPY bin/gitops-agent ./gitops-agent
# Repos are cloned at runtime into a volume mounted at /var/lib/gitops-repos
RUN mkdir -p /var/lib/gitops-repos
EXPOSE 8090
ENTRYPOINT ["./gitops-agent"]
