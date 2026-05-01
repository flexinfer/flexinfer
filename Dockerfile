# syntax=docker/dockerfile:1.7
# Build stage
ARG RUNTIME_REGISTRY=registry.harbor.lan
FROM golang:1.25.9-alpine AS builder

RUN apk add --no-cache git ca-certificates
ENV GOWORK=off \
    GOPRIVATE=gitlab.flexinfer.ai/* \
    GONOSUMDB=gitlab.flexinfer.ai/* \
    GONOPROXY=gitlab.flexinfer.ai/*

WORKDIR /src

# Copy go mod files
COPY go.mod go.sum ./
RUN --mount=type=secret,id=ci_job_token,required=false \
    --mount=type=secret,id=gitlab_token,required=false \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
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

# Build all binaries in a single layer to share build cache and parallelise MCP servers.
# The secret mount is repeated here because /go/pkg/mod is a cache mount (sharing=shared
# by default), which means concurrent image jobs can race it; if the cache is missing
# fi-mcp-kit's pseudo-version VCS metadata, `go build` falls back to `git ls-remote`
# and needs auth even though `go mod download` already ran. Re-applying git config is
# cheap and a no-op when the cache is intact.
ARG VERSION=dev
ARG MCP_BUILD_JOBS=4
RUN --mount=type=secret,id=ci_job_token,required=false \
    --mount=type=secret,id=gitlab_token,required=false \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
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
    mkdir -p /bin && \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loomd ./cmd/loomd && \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loom ./cmd/loom && \
    find cmd -mindepth 1 -maxdepth 1 -type d -name 'mcp-*' | xargs -n1 basename | \
      xargs -P"${MCP_BUILD_JOBS}" -I{} sh -c \
        'CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o "/bin/$1" "./cmd/$1"' _ {}

# Runtime stage - minimal image
FROM ${RUNTIME_REGISTRY}/dockerhub-cache/library/alpine:3.21

RUN apk add --no-cache ca-certificates git

# Create non-root user
RUN adduser -D -u 1000 mcp
USER mcp

# Copy binaries. Split so the stable mcp-* layer caches across commits while
# only the small loomd/loom layer rebuilds when the version ldflag changes.
COPY --from=builder /bin/mcp-* /usr/local/bin/
COPY --from=builder /bin/loomd /bin/loom /usr/local/bin/

# Default to running the daemon
ENTRYPOINT ["/usr/local/bin/loomd"]
CMD ["--registry", "/etc/loom/registry.yaml"]
