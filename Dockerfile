# syntax=docker/dockerfile:1.7
# Build stage
ARG RUNTIME_REGISTRY=registry.harbor.lan
FROM golang:1.25.7-alpine AS builder

RUN apk add --no-cache git ca-certificates

# This repo uses local `replace` directives to `../../libs/*`.
# In Docker builds, create those sibling directories at /libs/*.
RUN mkdir -p /libs
RUN --mount=type=secret,id=ci_job_token,required=false \
    --mount=type=secret,id=gitlab_token,required=false \
    set -eu; \
    token=""; \
    token_user=""; \
    if [ -s /run/secrets/ci_job_token ]; then \
      token="$(cat /run/secrets/ci_job_token)"; \
      token_user="gitlab-ci-token"; \
    elif [ -s /run/secrets/gitlab_token ]; then \
      token="$(cat /run/secrets/gitlab_token)"; \
      token_user="oauth2"; \
    fi; \
    if [ -n "$token" ]; then \
      base_url="https://${token_user}:${token}@gitlab.flexinfer.ai/libs"; \
    else \
      base_url="https://gitlab.flexinfer.ai/libs"; \
    fi; \
    git clone --depth 1 "${base_url}/mcp-go.git" /libs/mcp-go && \
    git clone --depth 1 "${base_url}/fi-mcp-kit.git" /libs/fi-mcp-kit && \
    git clone --depth 1 "${base_url}/fi-accel.git" /libs/fi-accel

WORKDIR /src

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build all binaries
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loomd ./cmd/loomd
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loom ./cmd/loom

# Build all MCP servers
RUN mkdir -p /bin && \
    for d in cmd/mcp-*; do \
      name="$(basename "$d")"; \
      CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w" -o "/bin/$name" "./$d"; \
    done

# Runtime stage - minimal image
FROM ${RUNTIME_REGISTRY}/dockerhub-cache/library/alpine:3.21

RUN apk add --no-cache ca-certificates git

# Create non-root user
RUN adduser -D -u 1000 mcp
USER mcp

# Copy binaries
COPY --from=builder /bin/loomd /bin/loom /bin/mcp-* /usr/local/bin/

# Default to running the daemon
ENTRYPOINT ["/usr/local/bin/loomd"]
CMD ["--registry", "/etc/loom/registry.yaml"]
