# Build stage
ARG RUNTIME_REGISTRY=registry.harbor.lan
FROM golang:1.25.5-alpine AS builder

RUN apk add --no-cache git ca-certificates

ARG CI_JOB_TOKEN

# This repo uses local `replace` directives to `../../libs/*`.
# In Docker builds, create those sibling directories at /libs/*.
RUN mkdir -p /libs
RUN if [ -n "${CI_JOB_TOKEN:-}" ]; then \
      git clone --depth 1 "https://gitlab-ci-token:${CI_JOB_TOKEN}@gitlab.flexinfer.ai/libs/mcp-go.git" /libs/mcp-go && \
      git clone --depth 1 "https://gitlab-ci-token:${CI_JOB_TOKEN}@gitlab.flexinfer.ai/libs/fi-mcp-kit.git" /libs/fi-mcp-kit ; \
    else \
      git clone --depth 1 "https://gitlab.flexinfer.ai/libs/mcp-go.git" /libs/mcp-go && \
      git clone --depth 1 "https://gitlab.flexinfer.ai/libs/fi-mcp-kit.git" /libs/fi-mcp-kit ; \
    fi

WORKDIR /src

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build all binaries
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loomd ./cmd/loomd
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loom ./cmd/loom

# Build all MCP servers
RUN mkdir -p /bin && \
    for d in cmd/mcp-*; do \
      name="$(basename "$d")"; \
      CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "/bin/$name" "./$d"; \
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
