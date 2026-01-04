# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build all binaries
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loomd ./cmd/loomd
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /bin/loom ./cmd/loom
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-time ./cmd/mcp-time
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-git ./cmd/mcp-git
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-github ./cmd/mcp-github
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-prometheus ./cmd/mcp-prometheus
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-tavily ./cmd/mcp-tavily
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-k8s ./cmd/mcp-k8s
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-k8s-ops ./cmd/mcp-k8s-ops
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-gitlab ./cmd/mcp-gitlab
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-grafana ./cmd/mcp-grafana
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-loki ./cmd/mcp-loki
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-minio ./cmd/mcp-minio
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-asus-router ./cmd/mcp-asus-router
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-cloudflare ./cmd/mcp-cloudflare
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-git-worktree ./cmd/mcp-git-worktree
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-memory ./cmd/mcp-memory
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-morph-embeddings ./cmd/mcp-morph-embeddings
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-morph-fast-apply ./cmd/mcp-morph-fast-apply
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-ops ./cmd/mcp-ops
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-qdrant ./cmd/mcp-qdrant
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-sequentialthinking ./cmd/mcp-sequentialthinking
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-server-mgmt ./cmd/mcp-server-mgmt
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-youtube ./cmd/mcp-youtube
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-zep ./cmd/mcp-zep
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/mcp-jira ./cmd/mcp-jira

# Runtime stage - minimal image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates git

# Create non-root user
RUN adduser -D -u 1000 mcp
USER mcp

# Copy binaries
COPY --from=builder /bin/loomd /bin/loom /bin/mcp-* /usr/local/bin/

# Default to running the daemon
ENTRYPOINT ["/usr/local/bin/loomd"]
CMD ["--registry", "/etc/loom/registry.yaml"]
