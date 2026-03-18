# syntax=docker/dockerfile:1.7
# Build stage
ARG RUNTIME_REGISTRY=registry.harbor.lan
FROM golang:1.25.8-alpine AS builder

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
      git config --global url."https://${token_user}:${token}@gitlab.flexinfer.ai/".insteadOf "https://gitlab.flexinfer.ai/"; \
    fi; \
    go mod download

# Copy source
COPY . .

# Build only the binaries required by the mobile-hud deployment.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loomd ./cmd/loomd
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loom ./cmd/loom
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w" -o /bin/mcp-agent-context ./cmd/mcp-agent-context
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w" -o /bin/mcp-hub-wrapper ./cmd/mcp-hub-wrapper

# Runtime stage - minimal image
FROM ${RUNTIME_REGISTRY}/dockerhub-cache/library/alpine:3.21

RUN apk add --no-cache ca-certificates git

# Create non-root user
RUN adduser -D -u 1000 mcp
USER mcp

# Copy only the binaries mobile-hud needs locally.
COPY --from=builder /bin/loomd /bin/loom /bin/mcp-agent-context /bin/mcp-hub-wrapper /usr/local/bin/

# Default to running the daemon
ENTRYPOINT ["/usr/local/bin/loomd"]
CMD ["--registry", "/etc/loom/registry.yaml"]
