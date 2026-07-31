# Binary is prebuilt by the CI go-builder stage (bin/portainer-agent, static
# linux/amd64). Do NOT recompile here: an in-container golang build pegs dind
# CPU/mem and drops the Jenkins agent channel mid-build (builds #138-#145).
# Mirror backend/Dockerfile — copy the prebuilt binary into a minimal runtime.

# Stage 1: pull terraform binary
FROM hashicorp/terraform:1.9 AS terraform

# Stage 2: minimal runtime image
FROM alpine:3.19
RUN printf '%s\n' \
      https://mirror.yandex.ru/mirrors/alpine/v3.19/main \
      https://mirror.yandex.ru/mirrors/alpine/v3.19/community \
      https://dl-cdn.alpinelinux.org/alpine/v3.19/main \
      https://dl-cdn.alpinelinux.org/alpine/v3.19/community \
      > /etc/apk/repositories \
 && { for i in 1 2 3; do apk add --no-cache ca-certificates openssh-client && exit 0; sleep 3; done; exit 1; }

COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY bin/portainer-agent /portainer-agent

EXPOSE 8090
ENTRYPOINT ["/portainer-agent"]
