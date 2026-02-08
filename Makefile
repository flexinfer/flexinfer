.PHONY: all build clean test install servers lint fmt vet check setup hooks dev help \
	ci ci-lint ci-lint-soft ci-lint-strict ci-build ci-test ci-test-unit ci-test-integration ci-test-race ci-benchmark \
	docker-build docker-build-loom-core docker-build-custom-server \
	docker-push docker-push-loom-core docker-push-custom-server \
	deploy deploy-status \
	hud hud-dev hud-build hud-frontend hud-clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
GOPATH := $(shell go env GOPATH)
GOLANGCI_LINT := $(GOPATH)/bin/golangci-lint
GOIMPORTS := $(GOPATH)/bin/goimports
GOSEC := $(GOPATH)/bin/gosec

# Docker settings
REGISTRY ?= registry.harbor.lan
LOOM_CORE_IMAGE := $(REGISTRY)/mcp/loom-core
CUSTOM_SERVER_IMAGE := $(REGISTRY)/mcp/custom-server
IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")

# Workspace root (for local Docker builds that need libs/)
WORKSPACE_ROOT ?= $(shell realpath ../.. 2>/dev/null || echo "$(HOME)/workspace")

# GitOps settings
GITOPS_DIR ?= $(shell realpath ../../platform/gitops 2>/dev/null || echo "$(HOME)/workspace/platform/gitops")
LOOM_HUB_DIR := $(GITOPS_DIR)/k3s/loom-hub

# MCP server binaries
MCP_SERVERS := mcp-time mcp-git mcp-github mcp-gitlab mcp-memory mcp-sequentialthinking mcp-prometheus mcp-k8s mcp-tavily mcp-server-mgmt mcp-cloudflare mcp-loki mcp-asus-router mcp-git-worktree mcp-grafana mcp-k8s-ops mcp-minio mcp-morph-embeddings mcp-qdrant mcp-ops mcp-zep mcp-morph-fast-apply mcp-youtube mcp-godot mcp-alertmanager mcp-flux mcp-postgres mcp-helm mcp-docker mcp-codebase-memory mcp-agent-context mcp-redis mcp-neo4j mcp-confluence mcp-browserkit
.PHONY: $(MCP_SERVERS)

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
	@echo "CI (local):"
	@echo "  make ci              - Run full CI pipeline locally"
	@echo "  make ci-lint         - Run CI lint stage (fmt, vet, lint - warnings only)"
	@echo "  make ci-lint-strict  - Run lint stage (fails on any issue)"
	@echo "  make ci-build        - Run CI build stage"
	@echo "  make ci-test         - Run CI test stage (unit + integration)"
	@echo "  make ci-test-unit    - Run unit tests with coverage threshold"
	@echo "  make ci-test-integration - Run integration tests"
	@echo "  make ci-benchmark    - Run benchmarks"
	@echo ""
	@echo "Docker:"
	@echo "  make docker-build              - Build all Docker images"
	@echo "  make docker-build-loom-core    - Build loom-core image"
	@echo "  make docker-build-custom-server - Build custom-server image"
	@echo "  make docker-push               - Push all images to registry"
	@echo "  make docker-push-loom-core     - Push loom-core image"
	@echo "  make docker-push-custom-server - Push custom-server image"
	@echo ""
	@echo "Deploy:"
	@echo "  make deploy         - Build, push, and deploy to k8s"
	@echo "  make deploy-status  - Show deployment status"
	@echo ""
	@echo "HUD (Agent Command Center):"
	@echo "  make hud           - Build frontend + Go binary, then launch HUD"
	@echo "  make hud-dev       - Launch HUD in dev mode (Vite hot-reload + Go API)"
	@echo "  make hud-build     - Build frontend (pnpm build) + Go binary"
	@echo "  make hud-frontend  - Build only the Svelte frontend"
	@echo "  make hud-clean     - Remove frontend node_modules and dist"
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

mcp-redis:
	go build $(LDFLAGS) -o bin/mcp-redis ./cmd/mcp-redis

mcp-neo4j:
	go build $(LDFLAGS) -o bin/mcp-neo4j ./cmd/mcp-neo4j

mcp-confluence:
	go build $(LDFLAGS) -o bin/mcp-confluence ./cmd/mcp-confluence

mcp-browserkit:
	go build $(LDFLAGS) -o bin/mcp-browserkit ./cmd/mcp-browserkit

clean: hud-clean
	rm -rf bin/
	rm -f coverage.out coverage.html

# Testing targets
test:
	go test ./...

test-sandbox:
	mkdir -p /tmp/go-build-cache
	GOCACHE=/tmp/go-build-cache go test ./...

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
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/boumenot/gocover-cobertura@v1.4.0
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

# =============================================================================
# CI TARGETS - Mimic GitLab CI pipeline locally
# =============================================================================

COVERAGE_THRESHOLD ?= 24

# Full CI pipeline
ci: ci-lint ci-build ci-test
	@echo ""
	@echo "✓ CI pipeline passed!"

# Lint stage (mirrors GitLab CI lint stage - lint allows failure in CI)
ci-lint: fmt-check vet ci-lint-soft
	@echo "✓ Lint stage passed (lint issues are warnings only, matching CI)"

# Lint soft - matches CI behavior (allow_failure: true)
ci-lint-soft:
	@echo "Running golangci-lint (warnings only, matches CI)..."
	-$(GOLANGCI_LINT) run --timeout 5m ./...

# Lint strict - fails on any lint issue
ci-lint-strict: fmt-check vet
	@echo "Running golangci-lint (strict)..."
	$(GOLANGCI_LINT) run --timeout 5m ./...
	@echo "✓ Lint stage passed (strict)"

# Build stage (mirrors GitLab CI build:binaries)
ci-build: clean
	@echo "Building all binaries..."
	@mkdir -p bin
	@VERSION=$$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
	LDFLAGS="-s -w -X main.version=$$VERSION"; \
	echo "Version: $$VERSION"; \
	CGO_ENABLED=0 go build -ldflags="$$LDFLAGS" -o bin/loom ./cmd/loom && \
	CGO_ENABLED=0 go build -ldflags="$$LDFLAGS" -o bin/loomd ./cmd/loomd && \
	failures=0; \
	for pkg in $$(ls cmd/ | grep '^mcp-'); do \
		echo "Building $$pkg..."; \
		if ! CGO_ENABLED=0 go build -ldflags="$$LDFLAGS" -o "bin/$$pkg" "./cmd/$$pkg"; then \
			echo "ERROR: Failed to build $$pkg"; \
			failures=$$((failures + 1)); \
		fi; \
	done; \
	if [ "$$failures" -ne 0 ]; then \
		echo "ERROR: $$failures MCP server(s) failed to build"; \
		exit 1; \
	fi
	@echo "Built binaries:"
	@ls -la bin/
	@echo "✓ Build stage passed"

# Test stage (mirrors GitLab CI test stage)
ci-test: ci-test-unit ci-test-integration
	@echo "✓ Test stage passed"

# Unit tests with coverage (mirrors GitLab CI test:unit)
ci-test-unit:
	@echo "Running unit tests with race detector and coverage..."
	@PKGS=$$(go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' ./... | sed '/^$$/d'); \
	if [ -z "$$PKGS" ]; then \
		echo "No packages with tests found"; \
		exit 0; \
	fi; \
	go test -race -coverprofile=coverage.out -covermode=atomic $$PKGS
	@echo ""
	@echo "Coverage Summary:"
	@go tool cover -func=coverage.out | tail -20
	@TOTAL=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo ""; \
	echo "Total Coverage: $${TOTAL}%"; \
	TOTAL_INT=$$(echo "$$TOTAL" | cut -d. -f1); \
	if [ "$$TOTAL_INT" -lt "$(COVERAGE_THRESHOLD)" ]; then \
		echo "ERROR: Coverage $${TOTAL}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	fi; \
	echo "✓ Coverage threshold met (>= $(COVERAGE_THRESHOLD)%)"

# Integration tests (mirrors GitLab CI test:integration)
ci-test-integration: ci-build
	@echo "Running integration tests..."
	@export LOOM_REPO_ROOT="$$(pwd)"; \
	export LOOM_RUN_MCP_SMOKE="1"; \
	export PATH="$$(pwd)/bin:$$PATH"; \
	echo "Available MCP servers:"; \
	ls -la bin/mcp-* 2>/dev/null || echo "No MCP servers found"; \
	echo ""; \
	go test -v -race ./internal/integration/...

# Race detection tests (mirrors GitLab CI test:race)
ci-test-race:
	@echo "Running race detection tests..."
	go test -race -short ./...
	@echo "✓ Race tests passed"

# Benchmarks (mirrors GitLab CI test:benchmark)
ci-benchmark: ci-build
	@echo "Running benchmarks..."
	@export LOOM_REPO_ROOT="$$(pwd)"; \
	export PATH="$$(pwd)/bin:$$PATH"; \
	go test -bench=. -benchmem -run=^$$ ./internal/... ./pkg/... 2>&1 | tee benchmark.txt
	@echo "Benchmark results saved to benchmark.txt"

# =============================================================================
# HUD TARGETS - Agent Command Center (Go HTTP + Svelte 5)
# =============================================================================

HUD_FRONTEND := internal/hud/frontend

# Build the Svelte frontend (requires pnpm)
hud-frontend:
	@echo "Building HUD frontend..."
	@if ! command -v pnpm >/dev/null 2>&1; then \
		echo "ERROR: pnpm is required. Install with: npm install -g pnpm"; \
		exit 1; \
	fi
	@if [ ! -d "$(HUD_FRONTEND)/node_modules" ]; then \
		echo "Installing frontend dependencies..."; \
		cd $(HUD_FRONTEND) && pnpm install; \
	fi
	cd $(HUD_FRONTEND) && pnpm build
	@echo "✓ Frontend built to $(HUD_FRONTEND)/dist/"

# Build frontend + Go binary with HUD embedded
hud-build: hud-frontend loom
	@echo "✓ HUD build complete"
	@echo "  Run: ./bin/loom hud"

# Launch HUD (builds first if needed)
hud: hud-build
	@echo "Launching HUD..."
	./bin/loom hud

# Dev mode: start Vite dev server + Go API concurrently
hud-dev: loom
	@echo "Starting HUD in development mode..."
	@echo "  Frontend: http://localhost:5173 (Vite)"
	@echo "  API:      http://localhost:9800 (Go)"
	@echo ""
	@if [ ! -d "$(HUD_FRONTEND)/node_modules" ]; then \
		echo "Installing frontend dependencies..."; \
		cd $(HUD_FRONTEND) && pnpm install; \
	fi
	@trap 'kill 0' EXIT; \
	./bin/loom hud --dev --port 9800 & \
	cd $(HUD_FRONTEND) && pnpm dev & \
	wait

# Clean frontend artifacts
hud-clean:
	@echo "Cleaning HUD frontend..."
	rm -rf $(HUD_FRONTEND)/node_modules $(HUD_FRONTEND)/dist
	@echo "✓ HUD cleaned"

# =============================================================================
# DOCKER TARGETS
# =============================================================================

# Build all Docker images (uses local Dockerfiles with workspace context)
docker-build: docker-build-loom-core docker-build-custom-server
	@echo "✓ All Docker images built"

# Build loom-core image (local build using workspace root context)
docker-build-loom-core:
	@echo "Building loom-core image..."
	@echo "Image: $(LOOM_CORE_IMAGE):$(IMAGE_TAG)"
	@echo "Context: $(WORKSPACE_ROOT)"
	cd $(WORKSPACE_ROOT) && docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(LOOM_CORE_IMAGE):$(IMAGE_TAG) \
		-t $(LOOM_CORE_IMAGE):latest \
		-f services/loom-core/Dockerfile.local .
	@echo "✓ loom-core image built"

# Build custom-server image (local build using workspace root context)
docker-build-custom-server:
	@echo "Building custom-server image..."
	@echo "Image: $(CUSTOM_SERVER_IMAGE):$(IMAGE_TAG)"
	@echo "Context: $(WORKSPACE_ROOT)"
	cd $(WORKSPACE_ROOT) && docker build \
		-t $(CUSTOM_SERVER_IMAGE):$(IMAGE_TAG) \
		-t $(CUSTOM_SERVER_IMAGE):latest \
		-f services/loom-core/Dockerfile.custom-server.local .
	@echo "✓ custom-server image built"

# Push all images
docker-push: docker-push-loom-core docker-push-custom-server
	@echo "✓ All images pushed to $(REGISTRY)"

# Push loom-core image
docker-push-loom-core: docker-build-loom-core
	@echo "Pushing loom-core image..."
	docker push $(LOOM_CORE_IMAGE):$(IMAGE_TAG)
	docker push $(LOOM_CORE_IMAGE):latest
	@echo "✓ loom-core pushed"

# Push custom-server image
docker-push-custom-server: docker-build-custom-server
	@echo "Pushing custom-server image..."
	docker push $(CUSTOM_SERVER_IMAGE):$(IMAGE_TAG)
	docker push $(CUSTOM_SERVER_IMAGE):latest
	@echo "✓ custom-server pushed"

# =============================================================================
# DEPLOY TARGETS
# =============================================================================

# Full deploy: build, push, update gitops, reconcile
deploy: docker-push deploy-update-images deploy-reconcile
	@echo ""
	@echo "✓ Deployment complete!"
	@echo "  Image tag: $(IMAGE_TAG)"
	@echo "  Registry:  $(REGISTRY)"

# Update image tags in gitops repo (only loom-hub/servers deployments)
deploy-update-images:
	@echo "Updating image tags in gitops repo..."
	@if [ ! -d "$(LOOM_HUB_DIR)/servers" ]; then \
		echo "ERROR: GitOps directory not found: $(LOOM_HUB_DIR)/servers"; \
		echo "Set GITOPS_DIR to override"; \
		exit 1; \
	fi
	@echo "Updating deployments to use $(CUSTOM_SERVER_IMAGE):$(IMAGE_TAG)"
	@for f in $(LOOM_HUB_DIR)/servers/*/deployment.yaml; do \
		if [ -f "$$f" ]; then \
			sed -i '' 's|$(REGISTRY)/mcp/custom-server:[a-zA-Z0-9._-]*|$(CUSTOM_SERVER_IMAGE):$(IMAGE_TAG)|g' "$$f"; \
		fi; \
	done
	@echo "✓ Image tags updated"
	@echo ""
	@echo "Changed files:"
	@cd $(GITOPS_DIR) && git diff --name-only k3s/loom-hub/

# Reconcile Flux
deploy-reconcile:
	@echo "Reconciling Flux..."
	@if command -v flux >/dev/null 2>&1; then \
		flux reconcile kustomization loom-hub -n flux-system --with-source; \
	else \
		echo "Warning: flux CLI not found, skipping reconcile"; \
		echo "Run manually: flux reconcile kustomization loom-hub -n flux-system --with-source"; \
	fi

# Commit gitops changes
deploy-commit:
	@echo "Committing gitops changes..."
	@cd $(GITOPS_DIR) && \
		git add k3s/loom-hub && \
		git commit -m "chore(loom-hub): update custom-server to $(IMAGE_TAG)" && \
		git push
	@echo "✓ GitOps changes committed and pushed"

# Show deployment status
deploy-status:
	@echo "=== Deployment Status ==="
	@echo ""
	@echo "Local:"
	@echo "  Version:   $(VERSION)"
	@echo "  Image tag: $(IMAGE_TAG)"
	@echo "  Registry:  $(REGISTRY)"
	@echo ""
	@echo "GitOps ($(LOOM_HUB_DIR)):"
	@if [ -d "$(LOOM_HUB_DIR)" ]; then \
		echo "  Current image tags:"; \
		grep -r "$(REGISTRY)/mcp/custom-server:" $(LOOM_HUB_DIR)/servers/*/deployment.yaml 2>/dev/null | \
			sed 's|.*/servers/||' | sed 's|/deployment.yaml:.*image: | -> |' | sort -u | head -10; \
	else \
		echo "  Directory not found"; \
	fi
	@echo ""
	@echo "Kubernetes:"
	@if command -v kubectl >/dev/null 2>&1; then \
		kubectl get pods -n loom-hub -o wide 2>/dev/null | head -15 || echo "  Unable to connect to cluster"; \
	else \
		echo "  kubectl not found"; \
	fi
