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
