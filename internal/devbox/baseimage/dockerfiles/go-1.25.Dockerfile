# Pre-built Go 1.25 base image for devbox sandboxes.
# Built by scripts/build-base-images.sh and pushed to Harbor.
FROM golang:1.25-alpine

RUN apk add --no-cache \
    ca-certificates git make bash curl \
    gcc musl-dev

# Common Go tools pinned to keep base image rebuilds reproducible.
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 && \
    go install golang.org/x/tools/cmd/goimports@v0.43.0

WORKDIR /workspace
CMD ["sleep", "infinity"]
