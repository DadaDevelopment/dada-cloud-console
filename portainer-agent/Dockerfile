# Stage 1: pull terraform binary
FROM hashicorp/terraform:1.9 AS terraform

# Stage 2: build Go binary
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /portainer-agent ./cmd/portainer-agent

# Stage 3: minimal runtime image
FROM alpine:3.19
RUN apk add --no-cache ca-certificates openssh-client

COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY --from=builder /portainer-agent /portainer-agent

EXPOSE 8090
ENTRYPOINT ["/portainer-agent"]
