# Pre-built Go 1.25 base image for devbox sandboxes.
# Built by scripts/build-base-images.sh and pushed to Harbor.
FROM golang:1.25-alpine

RUN apk add --no-cache \
    ca-certificates git make bash curl \
    gcc musl-dev

# Common Go tools
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest && \
    go install golang.org/x/tools/cmd/goimports@latest

WORKDIR /workspace
CMD ["sleep", "infinity"]
