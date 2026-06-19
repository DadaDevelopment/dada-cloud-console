# Binary is prebuilt by the CI go-builder stage (bin/portainer-agent, static
# linux/amd64). Do NOT recompile here: an in-container golang build pegs dind
# CPU/mem and drops the Jenkins agent channel mid-build (builds #138-#145).
# Mirror backend/Dockerfile — copy the prebuilt binary into a minimal runtime.

# Stage 1: pull terraform binary
FROM hashicorp/terraform:1.9 AS terraform

# Stage 2: minimal runtime image
FROM alpine:3.19
RUN apk add --no-cache ca-certificates openssh-client

COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY bin/portainer-agent /portainer-agent

EXPOSE 8090
ENTRYPOINT ["/portainer-agent"]
