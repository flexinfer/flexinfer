# Pre-built Go 1.24 base image for devbox sandboxes.
# Built by scripts/build-base-images.sh and pushed to Harbor.
FROM golang:1.24-alpine
ENV PATH="/usr/local/go/bin:${PATH}"

RUN apk add --no-cache \
    ca-certificates git make bash curl \
    gcc musl-dev

# Common Go tools pinned for Go 1.24 compatibility.
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 && \
    go install golang.org/x/tools/cmd/goimports@v0.42.0

WORKDIR /workspace
CMD ["sleep", "infinity"]
