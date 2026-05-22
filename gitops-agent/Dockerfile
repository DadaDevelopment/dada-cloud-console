FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o gitops-agent ./cmd/gitops-agent

FROM alpine:3.19
RUN apk add --no-cache ca-certificates git openssh-client
WORKDIR /app
COPY --from=builder /build/gitops-agent ./gitops-agent
# Repos are cloned at runtime into a volume mounted at /var/lib/gitops-repos
RUN mkdir -p /var/lib/gitops-repos
EXPOSE 8090
ENTRYPOINT ["./gitops-agent"]
