.PHONY: all build clean test install servers lint fmt vet check setup hooks dev help

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
GOPATH := $(shell go env GOPATH)
GOLANGCI_LINT := $(GOPATH)/bin/golangci-lint
GOIMPORTS := $(GOPATH)/bin/goimports
GOSEC := $(GOPATH)/bin/gosec

# MCP server binaries
MCP_SERVERS := mcp-time mcp-git mcp-github mcp-gitlab mcp-memory mcp-sequentialthinking mcp-prometheus mcp-k8s mcp-tavily mcp-server-mgmt mcp-cloudflare mcp-loki mcp-asus-router mcp-git-worktree mcp-grafana mcp-k8s-ops mcp-minio mcp-morph-embeddings mcp-qdrant mcp-ops mcp-zep mcp-morph-fast-apply mcp-youtube mcp-godot mcp-alertmanager mcp-flux mcp-postgres mcp-helm mcp-docker mcp-codebase-memory mcp-agent-context

# Default target
all: build

# Help target
help:
	@echo "Loom Core - MCP Server Framework"
	@echo ""
	@echo "Development:"
	@echo "  make setup      - Install dev dependencies and git hooks"
	@echo "  make hooks      - Install git pre-commit hooks"
	@echo "  make dev        - Build and run daemon in debug mode"
	@echo "  make check      - Run all checks (fmt, vet, lint, test)"
	@echo ""
	@echo "Building:"
	@echo "  make build      - Build all binaries"
	@echo "  make loom       - Build loom CLI"
	@echo "  make loomd      - Build loom daemon"
	@echo "  make servers    - Build all MCP servers"
	@echo ""
	@echo "Quality:"
	@echo "  make fmt        - Format code with gofmt"
	@echo "  make vet        - Run go vet"
	@echo "  make lint       - Run golangci-lint"
	@echo "  make lint-fix   - Run golangci-lint with auto-fix"
	@echo ""
	@echo "Testing:"
	@echo "  make test       - Run tests"
	@echo "  make test-cover - Run tests with coverage report"
	@echo "  make test-race  - Run tests with race detector"
	@echo ""
	@echo "Other:"
	@echo "  make install    - Install binaries to ~/.local/bin"
	@echo "  make clean      - Remove build artifacts"

build: loomd loom servers

loomd:
	go build $(LDFLAGS) -o bin/loomd ./cmd/loomd

loom:
	go build $(LDFLAGS) -o bin/loom ./cmd/loom

servers: $(MCP_SERVERS)

mcp-time:
	go build $(LDFLAGS) -o bin/mcp-time ./cmd/mcp-time

mcp-k8s:
	go build $(LDFLAGS) -o bin/mcp-k8s ./cmd/mcp-k8s

mcp-git:
	go build $(LDFLAGS) -o bin/mcp-git ./cmd/mcp-git

mcp-github:
	go build $(LDFLAGS) -o bin/mcp-github ./cmd/mcp-github

mcp-gitlab:
	go build $(LDFLAGS) -o bin/mcp-gitlab ./cmd/mcp-gitlab

mcp-memory:
	go build $(LDFLAGS) -o bin/mcp-memory ./cmd/mcp-memory

mcp-sequentialthinking:
	go build $(LDFLAGS) -o bin/mcp-sequentialthinking ./cmd/mcp-sequentialthinking

mcp-prometheus:
	go build $(LDFLAGS) -o bin/mcp-prometheus ./cmd/mcp-prometheus

mcp-tavily:
	go build $(LDFLAGS) -o bin/mcp-tavily ./cmd/mcp-tavily

mcp-server-mgmt:
	go build $(LDFLAGS) -o bin/mcp-server-mgmt ./cmd/mcp-server-mgmt

mcp-cloudflare:
	go build $(LDFLAGS) -o bin/mcp-cloudflare ./cmd/mcp-cloudflare

mcp-loki:
	go build $(LDFLAGS) -o bin/mcp-loki ./cmd/mcp-loki

mcp-asus-router:
	go build $(LDFLAGS) -o bin/mcp-asus-router ./cmd/mcp-asus-router

mcp-git-worktree:
	go build $(LDFLAGS) -o bin/mcp-git-worktree ./cmd/mcp-git-worktree

mcp-grafana:
	go build $(LDFLAGS) -o bin/mcp-grafana ./cmd/mcp-grafana

mcp-k8s-ops:
	go build $(LDFLAGS) -o bin/mcp-k8s-ops ./cmd/mcp-k8s-ops

mcp-minio:
	go build $(LDFLAGS) -o bin/mcp-minio ./cmd/mcp-minio

mcp-morph-embeddings:
	go build $(LDFLAGS) -o bin/mcp-morph-embeddings ./cmd/mcp-morph-embeddings

mcp-qdrant:
	go build $(LDFLAGS) -o bin/mcp-qdrant ./cmd/mcp-qdrant

mcp-ops:
	go build $(LDFLAGS) -o bin/mcp-ops ./cmd/mcp-ops

mcp-zep:
	go build $(LDFLAGS) -o bin/mcp-zep ./cmd/mcp-zep

mcp-morph-fast-apply:
	go build $(LDFLAGS) -o bin/mcp-morph-fast-apply ./cmd/mcp-morph-fast-apply

mcp-youtube:
	go build $(LDFLAGS) -o bin/mcp-youtube ./cmd/mcp-youtube

mcp-godot:
	go build $(LDFLAGS) -o bin/mcp-godot ./cmd/mcp-godot

mcp-alertmanager:
	go build $(LDFLAGS) -o bin/mcp-alertmanager ./cmd/mcp-alertmanager

mcp-flux:
	go build $(LDFLAGS) -o bin/mcp-flux ./cmd/mcp-flux

mcp-postgres:
	go build $(LDFLAGS) -o bin/mcp-postgres ./cmd/mcp-postgres

mcp-helm:
	go build $(LDFLAGS) -o bin/mcp-helm ./cmd/mcp-helm

mcp-docker:
	go build $(LDFLAGS) -o bin/mcp-docker ./cmd/mcp-docker

mcp-codebase-memory:
	go build $(LDFLAGS) -o bin/mcp-codebase-memory ./cmd/mcp-codebase-memory

mcp-agent-context:
	go build $(LDFLAGS) -o bin/mcp-agent-context ./cmd/mcp-agent-context

clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Testing targets
test:
	go test ./...

test-v:
	go test -v ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@echo "\nTo view HTML report: go tool cover -html=coverage.out"

test-coverage: test-cover
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-race:
	go test -race ./...

test-short:
	go test -short ./...

# Installation
install: build
	mkdir -p $(HOME)/.local/bin
	cp bin/loomd $(HOME)/.local/bin/
	cp bin/loom $(HOME)/.local/bin/
	cp bin/mcp-* $(HOME)/.local/bin/

# Code quality
fmt:
	gofmt -w ./cmd ./internal ./pkg

fmt-check:
	@test -z "$$(gofmt -l ./cmd ./internal ./pkg)" || (echo "Files need formatting:" && gofmt -l ./cmd ./internal ./pkg && exit 1)

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run --timeout 5m ./...

lint-fix:
	$(GOLANGCI_LINT) run --fix --timeout 5m ./...

# Run all checks (CI-like)
check: fmt-check vet lint test
	@echo "\nAll checks passed!"

# Quick check (faster for pre-commit)
check-quick: fmt-check vet
	$(GOLANGCI_LINT) run --fast ./...
	go build ./...

# Setup development environment
setup: tools hooks
	@echo "\nDevelopment environment ready!"
	@echo "Run 'make help' to see available commands"

# Install development tools
tools:
	@echo "Installing development tools..."
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOPATH)/bin latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	@echo "Tools installed to $(GOPATH)/bin"

# Install git hooks
hooks:
	@echo "Installing git hooks..."
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
		echo "pre-commit hooks installed"; \
	else \
		cp scripts/hooks/pre-commit .git/hooks/pre-commit; \
		chmod +x .git/hooks/pre-commit; \
		echo "Native pre-commit hook installed"; \
		echo "Tip: Install pre-commit for more features: pip install pre-commit"; \
	fi

# Pre-commit (run manually)
pre-commit:
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit run --all-files; \
	else \
		./scripts/hooks/pre-commit; \
	fi

# Security scanning
security:
	$(GOSEC) -fmt json -out gosec-report.json ./... || true
	@echo "Security report: gosec-report.json"

# Development mode
.PHONY: dev
dev: build
	./bin/loomd --debug

# Watch mode (requires entr: brew install entr)
watch:
	@echo "Watching for changes... (requires 'entr')"
	find . -name '*.go' -not -path './.go/*' | entr -r make dev

# Module maintenance
mod-tidy:
	go mod tidy

mod-verify:
	go mod verify

mod-update:
	go get -u ./...
	go mod tidy
