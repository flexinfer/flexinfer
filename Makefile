.PHONY: all build clean test install servers lint fmt vet check setup hooks git-setup dev help \
		loom loomd \
		install-core install-all bootstrap-local dev-sync dev-sync-repo dev-upgrade dev-reload \
	ci ci-lint ci-guardrails ci-lint-soft ci-lint-strict ci-build ci-test ci-test-unit ci-test-integration ci-test-enterprise-smoke ci-test-race ci-benchmark ci-security ci-baseline ci-contracts \
	codebase-bench-baseline codebase-bench-full codebase-bench-incremental codebase-bench-watch \
		security security-gosec security-vuln \
		changelog changelog-html changelog-json \
		docker-build docker-build-loom-core docker-build-custom-server \
		docker-push docker-push-loom-core docker-push-custom-server \
		deploy deploy-check deploy-status \
	browserkit-check browserkit-setup \
	hud hud-dev hud-build hud-install hud-install-service hud-reload hud-frontend hud-dist-check hud-clean \
		mobile-iphone-preflight mobile-gateway-sync-token mobile-gateway-preflight mobile-gateway-configure-url mobile-ios-project-sync mobile-hud mobile-app-open mobile-app-run-sim mobile-app-run-device mobile-dev mobile-gateway-dev \
		mobile-signing-check mobile-signing-prepare mobile-signing-cleanup mobile-app-archive-export \
	accel accel-build accel-verify

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
INSTALL_DIR ?= $(HOME)/.local/bin
GOPATH := $(shell go env GOPATH)
GOLANGCI_LINT := $(GOPATH)/bin/golangci-lint
GOIMPORTS := $(GOPATH)/bin/goimports
GOSEC := $(GOPATH)/bin/gosec
GOVULNCHECK := $(GOPATH)/bin/govulncheck
BASELINE_DIR ?= .loom/baselines
CODEBASE_BENCH_DIR ?= .loom/codebase-bench
LOOM_BUILD_P ?= 1
CGO_ENABLED ?= 0
MOBILE_IOS_PROJECT ?= apps/loom-companion-ios/LoomCompanion.xcodeproj
MOBILE_IOS_SCHEME ?= LoomCompanion
MOBILE_IOS_APP_NAME ?= LoomCompanion
MOBILE_IOS_BUNDLE_ID ?= ai.flexinfer.loom.companion
MOBILE_IOS_SIMULATOR ?= iPhone 17
MOBILE_IOS_CONFIGURATION ?= Debug
MOBILE_IOS_DERIVED_DATA ?= /tmp/loom-mobile-deriveddata
MOBILE_IOS_PROJECT_YAML ?= apps/loom-companion-ios/project.yml

# Docker settings
REGISTRY ?= registry.harbor.lan
LOOM_CORE_IMAGE := $(REGISTRY)/mcp/loom-core
CUSTOM_SERVER_IMAGE := $(REGISTRY)/mcp/custom-server
IMAGE_TAG ?= $(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "dev")

# Workspace root (for local Docker builds that need libs/)
WORKSPACE_ROOT ?= $(shell realpath ../.. 2>/dev/null || echo "$(HOME)/workspace")

# GitOps settings
GITOPS_DIR ?= $(shell realpath ../../platform/gitops 2>/dev/null || echo "$(HOME)/workspace/platform/gitops")
LOOM_HUB_DIR := $(GITOPS_DIR)/k3s/loom-hub
FLUX_KUST := $(GITOPS_DIR)/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml

# MCP server binaries
MCP_SERVERS := mcp-time mcp-git mcp-github mcp-gitlab mcp-memory mcp-sequentialthinking mcp-prometheus mcp-k8s mcp-tavily mcp-server-mgmt mcp-cloudflare mcp-loki mcp-asus-router mcp-git-worktree mcp-grafana mcp-k8s-ops mcp-minio mcp-morph-embeddings mcp-qdrant mcp-quality mcp-ops mcp-zep mcp-morph-fast-apply mcp-youtube mcp-godot mcp-alertmanager mcp-flux mcp-postgres mcp-helm mcp-docker mcp-codebase-memory mcp-agent-context mcp-redis mcp-neo4j mcp-confluence mcp-browserkit mcp-devbox mcp-itchio mcp-release mcp-substack mcp-linkedin mcp-google-workspace mcp-jobsearch mcp-mentatlab mcp-flexinfer mcp-weaver
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
	@echo "  make git-setup  - Repair worktree-aware git config and install shared hooks"
	@echo "  make dev        - Build and run daemon in debug mode"
	@echo "  make dev-sync   - Regen configs and sync all profiles + skills using the repo-built loom"
	@echo "  make dev-sync-repo - Regen configs + skills in-repo only (skip home sync/install)"
	@echo "  make dev-upgrade - Build, install, sync configs+skills, restart daemon (safe when idle; direct embedded-HUD fallback when launchd is not active)"
	@echo "  make dev-reload  - Build, install, sync configs+skills, force-restart daemon (embedded HUD included)"
	@echo "  make bootstrap-local - Build + install core binaries + sync configs+skills + check setup"
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
	@echo "  make ci-guardrails   - Run docs drift + loom CLI help smoke checks"
	@echo "  make ci-lint-strict  - Run lint stage (fails on any issue)"
	@echo "  make ci-build        - Run CI build stage"
	@echo "  make ci-test         - Run CI test stage (unit + integration)"
	@echo "  make ci-test-unit    - Run unit tests with coverage threshold"
	@echo "  make ci-test-integration - Run integration tests"
	@echo "  make ci-test-enterprise-smoke - Run enterprise smoke suite (gateway + RBAC + devbox)"
	@echo "  make ci-benchmark    - Run benchmarks"
	@echo "  make ci-security     - Run CI security stage (gosec + govulncheck)"
	@echo "  make ci-baseline     - Capture benchmark + health baseline artifacts"
	@echo "  make codebase-bench-baseline    - Run full + incremental + watch codebase benchmarks"
	@echo "  make codebase-bench-full        - Run full-refresh codebase benchmark"
	@echo "  make codebase-bench-incremental - Run unchanged-rerun codebase benchmark"
	@echo "  make codebase-bench-watch       - Run watch-latency benchmark on fixture repo"
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
	@echo "  make deploy-check   - Run deploy validation gates"
	@echo "  make deploy-status  - Show deployment status"
	@echo ""
	@echo "HUD (Agent Command Center):"
	@echo "  make hud           - Build frontend + loomd, then launch with embedded HUD"
	@echo "  make hud-reload    - Full cycle: build, install, restart loomd with HUD"
	@echo "  make hud-dev       - Launch HUD in dev mode (Vite hot-reload + loomd API)"
	@echo "  make hud-build     - Build frontend (pnpm build) + Go binary"
	@echo "  make hud-install   - Build + install loom+loomd to ~/.local/bin"
	@echo "  make hud-install-service - Install HUD as launchd service (auto-start, Redis)"
	@echo "  make hud-frontend  - Build only the Svelte frontend"
	@echo "  make hud-clean     - Remove frontend node_modules and dist"
	@echo ""
	@echo "Mobile Companion (iPhone):"
	@echo "  make mobile-iphone-preflight - Verify Xcode + iOS device test prerequisites"
	@echo "  make mobile-gateway-sync-token - Sync local mobile token/scopes from loom-hub/loom-secrets"
	@echo "  make mobile-gateway-preflight - Verify MCP + mobile API surfaces on gateway host"
	@echo "  make mobile-gateway-configure-url - Echo loom://configure URL for Companion gateway bootstrap"
	@echo "  make mobile-ios-project-sync - Regenerate Xcode project from project.yml"
	@echo "  make mobile-hud              - Launch HUD with mobile auth on 0.0.0.0:3333"
	@echo "  make mobile-app-open         - Open iOS app project in Xcode"
	@echo "  make mobile-app-run-sim      - Build/install/launch app in iOS Simulator"
	@echo "  make mobile-app-run-device   - Build/install/launch app on connected iPhone"
	@echo "  make mobile-dev              - Generate token, restart HUD, open app, print URL+token"
	@echo "  make mobile-gateway-dev      - Rotate token, patch loom-hub secret, restart mobile-hud, verify gateway"
	@echo "  make mobile-signing-check    - Check iOS signing variables and current Xcode signing state"
	@echo "  make mobile-signing-prepare  - Import Apple cert/profile into a temporary keychain (streamslate-style)"
	@echo "  make mobile-signing-cleanup  - Remove temporary signing keychain and restore search list"
	@echo "  make mobile-app-archive-export - Archive + app-store export (requires signing env prepared)"
	@echo ""
	@echo "Changelog:"
	@echo "  make changelog       - Generate CHANGELOG.generated.md from git history (Keep-a-Changelog)"
	@echo "  make changelog-html  - Generate changelog as HTML with Aurora theme"
	@echo "  make changelog-json  - Generate changelog as JSON for programmatic use"
	@echo ""
	@echo "Schemas:"
	@echo "  make schemas-list    - List vendored upstream platform schemas"
	@echo "  make schemas-check   - Check for upstream schema drift"
	@echo "  make schemas-update  - Fetch and update vendored schemas from upstream"
	@echo ""
	@echo "Acceleration (fi-accel native library):"
	@echo "  make accel         - Rebuild fi-accel native lib + verify CGO link"
	@echo "  make accel-build   - Rebuild fi-accel-ffi from Rust source"
	@echo "  make accel-verify  - Verify CGO_ENABLED=1 build succeeds"
	@echo ""
	@echo "Other:"
	@echo "  make install    - Install binaries to ~/.local/bin"
	@echo "  make clean      - Remove build artifacts"
	@echo ""
	@echo "BrowserKit (local-only screenshots):"
	@echo "  make browserkit-check  - Verify Python deps + Playwright Chromium"
	@echo "  make browserkit-setup  - Install Python deps + Playwright Chromium (downloads)"

build: loomd loom servers mcp-hub-wrapper

loomd:
	go build $(LDFLAGS) -o bin/loomd ./cmd/loomd

loom:
	@# cmd/loom embeds internal/hud/frontend/dist via //go:embed.
	@# Go's build cache doesn't track embedded file content changes,
	@# so we must flush the cache when dist/ is newer than bin/loom.
	@if [ -d "$(HUD_FRONTEND)/dist" ] && [ -f bin/loom ]; then \
		newest_dist=$$(find $(HUD_FRONTEND)/dist -type f -newer bin/loom 2>/dev/null | head -1); \
		if [ -n "$$newest_dist" ]; then \
			echo "Embedded assets changed — flushing Go build cache for cmd/loom"; \
			go clean -cache; \
		fi; \
	fi
	CGO_ENABLED=$(CGO_ENABLED) go build -p $(LOOM_BUILD_P) $(LDFLAGS) -o bin/loom ./cmd/loom

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

mcp-quality:
	go build $(LDFLAGS) -o bin/mcp-quality ./cmd/mcp-quality

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

mcp-devbox:
	go build $(LDFLAGS) -o bin/mcp-devbox ./cmd/mcp-devbox

base-images:
	./scripts/build-base-images.sh --push

# sync-spawn-driver copies the loom-spawn-driver bundle from its source-of-truth
# location under tools/spawn-driver/dist/ into internal/hud/ where go:embed can
# pick it up. Run after editing the driver bundle (Slice 7c+ will replace the
# stub with an esbuild-generated bundle from TypeScript sources).
sync-spawn-driver:
	@cp tools/spawn-driver/dist/spawn-driver.js internal/hud/spawn_driver_bundle.js
	@echo "Synced spawn-driver bundle to internal/hud/spawn_driver_bundle.js"

install-devbox-sync:
	@mkdir -p $(HOME)/.config/loom/logs
	cp launchd/com.loom.devbox-sync.plist $(HOME)/Library/LaunchAgents/com.loom.devbox-sync.plist
	launchctl unload $(HOME)/Library/LaunchAgents/com.loom.devbox-sync.plist 2>/dev/null || true
	launchctl load $(HOME)/Library/LaunchAgents/com.loom.devbox-sync.plist
	@echo "Devbox sync agent installed and started."
	@echo "  Logs: $(HOME)/.config/loom/logs/devbox-sync.log"

mcp-itchio:
	go build $(LDFLAGS) -o bin/mcp-itchio ./cmd/mcp-itchio

mcp-release:
	go build $(LDFLAGS) -o bin/mcp-release ./cmd/mcp-release

mcp-substack:
	go build $(LDFLAGS) -o bin/mcp-substack ./cmd/mcp-substack

mcp-linkedin:
	go build $(LDFLAGS) -o bin/mcp-linkedin ./cmd/mcp-linkedin

mcp-google-workspace:
	go build $(LDFLAGS) -o bin/mcp-google-workspace ./cmd/mcp-google-workspace

mcp-jobsearch:
	go build $(LDFLAGS) -o bin/mcp-jobsearch ./cmd/mcp-jobsearch

mcp-mentatlab:
	go build $(LDFLAGS) -o bin/mcp-mentatlab ./cmd/mcp-mentatlab

mcp-flexinfer:
	go build $(LDFLAGS) -o bin/mcp-flexinfer ./cmd/mcp-flexinfer

mcp-weaver:
	go build $(LDFLAGS) -o bin/mcp-weaver ./cmd/mcp-weaver

mcp-hub-wrapper:
	go build $(LDFLAGS) -o bin/mcp-hub-wrapper ./cmd/mcp-hub-wrapper

clean: hud-clean
	rm -rf bin/
	rm -f coverage.out coverage.html

## Acceleration / fi-accel native library ————————————————————————————
FIACCEL_DIR ?= $(WORKSPACE_ROOT)/libs/fi-accel

accel: accel-build accel-verify ## Rebuild fi-accel native lib + verify CGO build

accel-build:
	@echo "Building fi-accel-ffi (release) ..."
	cd $(FIACCEL_DIR) && cargo build --release -p fi-accel-ffi
	yes | cp $(FIACCEL_DIR)/target/release/libfi_accel_ffi.a $(FIACCEL_DIR)/go/lib/darwin_arm64/libfi_accel_ffi.a
	@echo "Verifying eventlog symbols ..."
	@nm $(FIACCEL_DIR)/go/lib/darwin_arm64/libfi_accel_ffi.a 2>/dev/null | grep -q '_fi_project_eventlog' \
		|| { echo "ERROR: _fi_project_eventlog not found in rebuilt library"; exit 1; }
	@echo "fi-accel-ffi rebuilt successfully."

accel-verify:
	@echo "Verifying CGO build ..."
	CGO_ENABLED=1 go build ./cmd/loom ./cmd/loomd ./cmd/mcp-agent-context
	@echo "CGO build verified."

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
install: install-all

# Install only loom + loomd (fast iteration; least disruptive to agent/server processes).
install-core: loom loomd mcp-hub-wrapper
	@chmod +x scripts/install_atomic.sh
	@scripts/install_atomic.sh bin/loomd $(INSTALL_DIR)/loomd
	@scripts/install_atomic.sh bin/loom  $(INSTALL_DIR)/loom
	@scripts/install_atomic.sh bin/mcp-hub-wrapper $(INSTALL_DIR)/mcp-hub-wrapper

# Install loom, loomd, and all MCP server binaries.
install-all: build
	@chmod +x scripts/install_atomic.sh
	@mkdir -p $(INSTALL_DIR)
	@scripts/install_atomic.sh bin/loomd $(INSTALL_DIR)/loomd
	@scripts/install_atomic.sh bin/loom  $(INSTALL_DIR)/loom
	@scripts/install_atomic.sh bin/mcp-hub-wrapper $(INSTALL_DIR)/mcp-hub-wrapper
	@for f in bin/mcp-*; do \
		if [ -f "$$f" ]; then scripts/install_atomic.sh "$$f" "$(INSTALL_DIR)/$$(basename $$f)"; fi; \
	done
	@echo ""
	@echo "Note: mcp-browserkit is local-only and requires Python deps."
	@echo "  Run: make browserkit-check"
	@echo "  Or:  make browserkit-setup"

# Regenerate configs and sync all profiles + skills from the repo-built loom binary.
# This avoids PATH drift when the installed loom binary is stale.
dev-sync: loom
	@./bin/loom sync all --regen --loom-mode --loom-binary "$(PWD)/bin/loom"
	@./bin/loom sync skills all

# Regenerate configs and skills in-repo only. Useful in sandboxes or when home sync/install
# would fail, while still keeping repo-local generated artifacts fresh.
dev-sync-repo: loom
	@./bin/loom sync all --regen --repo-only --loom-mode --loom-binary "$(PWD)/bin/loom"
	@./bin/loom sync skills all --repo-only

# One-command local dev upgrade:
# - rebuild loom/loomd
# - atomic install to ~/.local/bin
# - regen+sync configs + skills in loom mode
# - restart daemon only when idle
# - use a direct embedded-HUD restart path when launchd is installed but not active
# - leave loom proxy clients running so they reconnect across daemon restarts
dev-upgrade:
	@chmod +x scripts/dev/upgrade_local.sh
	@scripts/dev/upgrade_local.sh

# Force rebuild + restart: always restarts daemon regardless of active connections.
# Leaves existing loom proxy clients running; they reconnect on the next tool call.
# Set REAP_PROXY_CLIENTS=always only when intentionally resetting client-held proxy processes.
# If launchd is not actively managing the daemon, restart directly with --hud-port so
# the embedded HUD/mobile API comes back in the same process.
dev-reload:
	@chmod +x scripts/dev/upgrade_local.sh
	@RESTART_DAEMON=always scripts/dev/upgrade_local.sh

# First-run/local onboarding:
# - build + atomic install loom/loomd
# - regenerate and sync loom-mode configs + skills
# - run environment checks
bootstrap-local: git-setup install-core
	@"$(INSTALL_DIR)/loom" sync all --regen --loom-mode --loom-binary "$(INSTALL_DIR)/loom"
	@"$(INSTALL_DIR)/loom" sync skills all
	@"$(INSTALL_DIR)/loom" check
	@echo ""
	@echo "Bootstrap complete."
	@echo "Next:"
	@echo "  loom start"
	@echo "  loom hud --port 3333"

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
setup: tools git-setup
	@echo "\nDevelopment environment ready!"
	@echo "Run 'make help' to see available commands"

# Install development tools
tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install github.com/boumenot/gocover-cobertura@v1.4.0
	@echo "Tools installed to $(GOPATH)/bin"

# Repair worktree-aware git config and install hooks into the shared git dir.
git-setup:
	@chmod +x scripts/dev/with-clean-git-env.sh
	@chmod +x scripts/dev/repair_git_setup.sh
	@chmod +x scripts/hooks/run-pre-commit-hook.sh
	@chmod +x scripts/hooks/with-stashed-worktree.sh
	@chmod +x scripts/hooks/pre-commit
	@chmod +x scripts/hooks/pre-commit-native.sh
	@chmod +x scripts/hooks/pre-push
	@chmod +x scripts/hooks/pre-push-native.sh
	@./scripts/dev/repair_git_setup.sh
	@if command -v pre-commit >/dev/null 2>&1; then \
		echo "git-setup: pre-commit detected"; \
	else \
		echo "git-setup: pre-commit not installed; native hook fallback will be used"; \
	fi

hooks: git-setup
	@echo "Hooks installed via git-setup"

# Pre-commit (run manually)
pre-commit:
	@if [ -f .pre-commit-config.yaml ] && command -v pre-commit >/dev/null 2>&1; then \
		bash scripts/dev/with-clean-git-env.sh pre-commit run --all-files; \
	else \
		./scripts/hooks/pre-commit-native.sh; \
	fi

# =============================================================================
# BrowserKit (local-only) prerequisites
# =============================================================================

browserkit-check:
	bash scripts/browserkit/check_ready.sh

browserkit-setup:
	bash scripts/browserkit/install_deps.sh

# Security scanning
security: security-gosec security-vuln
	@echo "✓ Security checks passed"

security-gosec:
	$(GOSEC) -fmt json -out gosec-report.json ./...
	@echo "Security report: gosec-report.json"

security-vuln:
	$(GOVULNCHECK) ./... > govulncheck-report.txt
	@cat govulncheck-report.txt
	@echo "Vulnerability report: govulncheck-report.txt"

# =============================================================================
# Schema Management
# =============================================================================

# List vendored upstream platform schemas
schemas-list: loom
	./bin/loom schemas list

# Check for upstream schema drift (report only)
schemas-check: loom
	./bin/loom schemas update

# Fetch and update vendored schemas from upstream, then rebuild
schemas-update: loom
	./bin/loom schemas update --apply
	@echo "Schemas updated. Rebuilding to embed new schemas..."
	$(MAKE) build

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

COVERAGE_THRESHOLD ?= 28

# Full CI pipeline
ci: ci-lint ci-build ci-test ci-contracts ci-security
	@echo ""
	@echo "✓ CI pipeline passed!"

# Lint stage (mirrors GitLab CI lint stage - lint allows failure in CI)
ci-lint: fmt-check vet ci-guardrails ci-lint-soft
	@echo "✓ Lint stage passed (lint issues are warnings only, matching CI)"

# Guardrails: keep docs and command surface in sync.
ci-guardrails:
	@echo "Running docs/CLI guardrails..."
	@bash scripts/ci/check_docs_guardrails.sh
	@bash scripts/ci/check_docs_guardrails_test.sh
	@bash scripts/ci/check_flexinfer_site_integration.sh
	@bash scripts/ci/check_error_handling.sh
	@go run ./cmd/loom --help >/dev/null
	@go run ./cmd/loom proxy --help >/dev/null
	@echo "✓ Guardrails passed"

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

# Contract tests — verify golden files are up-to-date (run in CI and before releases)
# Fails if any golden file would change, surfacing drift for sibling consumers (loom, loom-zed).
ci-contracts:
	@echo "Running contract tests (golden file verification)..."
	@go test -v -count=1 -run 'Contract$$' ./internal/contracts/...
	@echo ""
	@echo "Golden files verified:"
	@ls internal/contracts/testdata/*.golden | wc -l | xargs -I{} echo "  {} golden files checked"
	@echo "✓ Contract tests passed — no drift detected"

# Enterprise smoke suite (mirrors GitLab CI test:enterprise-smoke)
ci-test-enterprise-smoke:
	@echo "Running enterprise smoke suite..."
	@bash scripts/ci/enterprise_smoke_suite.sh

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

# Security stage (mirrors CI security jobs)
ci-security:
	@echo "Running security checks..."
	@if [ ! -x "$(GOSEC)" ]; then go install github.com/securego/gosec/v2/cmd/gosec@latest; fi
	@if [ ! -x "$(GOVULNCHECK)" ]; then go install golang.org/x/vuln/cmd/govulncheck@latest; fi
	@PATH="$(GOPATH)/bin:$$PATH" $(MAKE) security

# Baseline capture for perf/health tracking
ci-baseline: ci-build
	@echo "Capturing benchmark + health baseline..."
	@mkdir -p $(BASELINE_DIR)
	@TS=$$(date +%Y%m%d-%H%M%S); \
	export LOOM_REPO_ROOT="$$(pwd)"; \
	export PATH="$$(pwd)/bin:$$PATH"; \
	go test -bench=. -benchmem -run=^$$ ./internal/... ./pkg/... 2>&1 | tee "$(BASELINE_DIR)/benchmark-$$TS.txt"; \
	if command -v curl >/dev/null 2>&1 && curl -fsS http://localhost:9876/health > "$(BASELINE_DIR)/health-$$TS.json"; then \
		echo "Saved daemon health snapshot: $(BASELINE_DIR)/health-$$TS.json"; \
	else \
		echo "Skipped daemon health snapshot (loomd not running on http://localhost:9876/health)"; \
	fi
	@echo "Baseline artifacts saved in $(BASELINE_DIR)/"

codebase-bench-baseline:
	@echo "Running codebase benchmark baseline..."
	@mkdir -p $(CODEBASE_BENCH_DIR)
	go run ./cmd/codebase-bench \
		-scenario all \
		-root "$$(pwd)" \
		-output-dir "$(CODEBASE_BENCH_DIR)"

codebase-bench-full:
	@echo "Running full-refresh codebase benchmark..."
	@mkdir -p $(CODEBASE_BENCH_DIR)
	go run ./cmd/codebase-bench \
		-scenario full \
		-root "$$(pwd)" \
		-output-dir "$(CODEBASE_BENCH_DIR)"

codebase-bench-incremental:
	@echo "Running incremental codebase benchmark..."
	@mkdir -p $(CODEBASE_BENCH_DIR)
	go run ./cmd/codebase-bench \
		-scenario incremental \
		-root "$$(pwd)" \
		-output-dir "$(CODEBASE_BENCH_DIR)"

codebase-bench-watch:
	@echo "Running watch-latency codebase benchmark..."
	@mkdir -p $(CODEBASE_BENCH_DIR)
	go run ./cmd/codebase-bench \
		-scenario watch \
		-root "$$(pwd)" \
		-output-dir "$(CODEBASE_BENCH_DIR)"

# =============================================================================
# HUD TARGETS - Agent Command Center (Go HTTP + Svelte 5)
# =============================================================================

HUD_FRONTEND := internal/hud/frontend

# Build the Svelte frontend (requires pnpm)
# Always cleans dist/ first so stale assets never leak into the embed.
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
	rm -rf $(HUD_FRONTEND)/dist
	cd $(HUD_FRONTEND) && pnpm build
	@echo "✓ Frontend built to $(HUD_FRONTEND)/dist/"

# Build frontend + Go binary with HUD embedded.
# The loom target auto-detects stale embedded assets and flushes the
# Go build cache when needed, so a plain `make loom` is sufficient.
hud-build: hud-frontend loom
	@echo "✓ HUD build complete (bin/loom)"

# Build + install loom and loomd to ~/.local/bin in one step.
# HUD is embedded in loomd; loom is the proxy/CLI.
hud-install: hud-build loomd
	@chmod +x scripts/install_atomic.sh
	@scripts/install_atomic.sh bin/loom $(INSTALL_DIR)/loom
	@scripts/install_atomic.sh bin/loomd $(INSTALL_DIR)/loomd
	@echo "✓ Installed loom + loomd to $(INSTALL_DIR)/"
	@echo "  Restart HUD: loomd --hud-port 3333"

# Install HUD as a launchd service (auto-start on login, Redis cache).
hud-install-service: build
	@./bin/loom hud install

# Full cycle: build frontend, rebuild+install binaries, restart running loomd.
# This is the one-command target for HUD development iteration.
hud-reload: hud-install
	@echo "Restarting loomd (embedded HUD)..."
	@HUD_PID=$$(lsof -ti :3333 2>/dev/null | head -1); \
	if [ -n "$$HUD_PID" ]; then \
		kill $$HUD_PID 2>/dev/null || true; \
		sleep 1; \
		if kill -0 $$HUD_PID 2>/dev/null; then kill -9 $$HUD_PID 2>/dev/null || true; fi; \
		echo "Killed old process (PID $$HUD_PID)"; \
	else \
		echo "No process found on port 3333"; \
	fi; \
	echo "Starting loomd with embedded HUD..."; \
	nohup $(INSTALL_DIR)/loomd --hud-port 3333 > /tmp/loomd-hud.log 2>&1 & \
	NEW_PID=$$!; \
	sleep 3; \
	if kill -0 $$NEW_PID 2>/dev/null; then \
		HUD_URL=$$(bash scripts/dev/detect_hud_url.sh 3333); \
		echo "✓ loomd restarted (PID $$NEW_PID) — $$HUD_URL"; \
	else \
		echo "ERROR: loomd failed to start. Check /tmp/loomd-hud.log"; \
		exit 1; \
	fi

# Launch loomd with embedded HUD (builds first if needed)
hud: hud-build loomd
	@echo "Launching loomd with embedded HUD..."
	./bin/loomd --hud-port 3333

# Dev mode: start Vite dev server + Go API concurrently
hud-dev: loom loomd
	@echo "Starting HUD in development mode..."
	@echo "  Frontend: http://localhost:5173 (Vite)"
	@echo "  API:      http://localhost:9800 (Go)"
	@echo ""
	@if [ ! -d "$(HUD_FRONTEND)/node_modules" ]; then \
		echo "Installing frontend dependencies..."; \
		cd $(HUD_FRONTEND) && pnpm install; \
	fi
	@trap 'kill 0' EXIT; \
	./bin/loomd --hud-port 9800 & \
	cd $(HUD_FRONTEND) && pnpm dev & \
	wait

# Verify committed dist/ matches a fresh build.
# Use locally before committing or in CI (requires pnpm/node).
hud-dist-check: hud-frontend
	@echo "Checking HUD dist freshness..."
	@if git diff --quiet $(HUD_FRONTEND)/dist/ 2>/dev/null; then \
		echo "✓ HUD dist is up-to-date"; \
	else \
		echo "ERROR: HUD dist is stale. Run 'make hud-frontend' and commit the result."; \
		git diff --stat $(HUD_FRONTEND)/dist/; \
		exit 1; \
	fi

# Clean frontend artifacts
hud-clean:
	@echo "Cleaning HUD frontend..."
	rm -rf $(HUD_FRONTEND)/node_modules $(HUD_FRONTEND)/dist
	@echo "✓ HUD cleaned"

# Verify local prerequisites for running the iOS app on a physical iPhone.
mobile-iphone-preflight:
	@./scripts/mobile/iphone_preflight.sh

mobile-gateway-sync-token:
	@./scripts/mobile/gateway_sync_token.sh

mobile-gateway-preflight: mobile-gateway-sync-token
	@./scripts/mobile/gateway_preflight.sh

# Echo the `loom://configure?...` URL used by mobile-app-run-{sim,device}
# to bootstrap Gateway-mode creds on freshly installed builds. Reads from
# env vars, ~/.config/loom/hud.env, or the loom-hub/loom-secrets secret.
# Prints nothing (exit 0) if creds aren't resolvable — callers skip config.
mobile-gateway-configure-url:
	@./scripts/mobile/build_configure_url.sh; echo

# Keep the generated Xcode project aligned with project.yml and source layout.
mobile-ios-project-sync:
	@if ! command -v xcodegen >/dev/null 2>&1; then \
		echo "ERROR: xcodegen is required to generate $(MOBILE_IOS_PROJECT)"; \
		echo "Install with: brew install xcodegen"; \
		exit 1; \
	fi
	@cd apps/loom-companion-ios && xcodegen generate --use-cache >/tmp/loom-mobile-xcodegen.log 2>&1 || { \
		echo "ERROR: failed to generate iOS project from $(MOBILE_IOS_PROJECT_YAML)"; \
		tail -n 40 /tmp/loom-mobile-xcodegen.log; \
		exit 1; \
	}

# Open the iOS app project in Xcode.
mobile-app-open: mobile-ios-project-sync
	@open "$(MOBILE_IOS_PROJECT)"

# Build, install, and launch Loom Companion in iOS Simulator.
# Optional overrides:
#   MOBILE_IOS_SIMULATOR="iPhone 17 Pro"
#   MOBILE_IOS_CONFIGURATION=Debug
mobile-app-run-sim: mobile-ios-project-sync
	@echo "Booting simulator: $(MOBILE_IOS_SIMULATOR)"
	@open -a Simulator >/dev/null 2>&1 || true
	@xcrun simctl boot "$(MOBILE_IOS_SIMULATOR)" >/dev/null 2>&1 || true
	@xcrun simctl bootstatus "$(MOBILE_IOS_SIMULATOR)" -b >/dev/null 2>&1 || true
	@echo "Building $(MOBILE_IOS_SCHEME) for simulator..."
	@xcodebuild -project "$(MOBILE_IOS_PROJECT)" \
		-scheme "$(MOBILE_IOS_SCHEME)" \
		-destination "platform=iOS Simulator,name=$(MOBILE_IOS_SIMULATOR)" \
		-configuration "$(MOBILE_IOS_CONFIGURATION)" \
		-derivedDataPath "$(MOBILE_IOS_DERIVED_DATA)" \
		build >/tmp/loom-mobile-app-build.log && tail -n 10 /tmp/loom-mobile-app-build.log
	@APP_PATH="$(MOBILE_IOS_DERIVED_DATA)/Build/Products/$(MOBILE_IOS_CONFIGURATION)-iphonesimulator/$(MOBILE_IOS_APP_NAME).app"; \
	if [ ! -d "$$APP_PATH" ]; then \
		echo "ERROR: app bundle not found at $$APP_PATH"; \
		exit 1; \
	fi; \
	SIM_UDID="$$(xcrun simctl list devices booted | rg -o -m1 '[0-9A-F-]{36}')"; \
	if [ -z "$$SIM_UDID" ]; then \
		echo "No booted simulator detected; retrying boot for $(MOBILE_IOS_SIMULATOR)..."; \
		xcrun simctl boot "$(MOBILE_IOS_SIMULATOR)" >/dev/null 2>&1 || true; \
		xcrun simctl bootstatus "$(MOBILE_IOS_SIMULATOR)" -b >/dev/null 2>&1 || true; \
		SIM_UDID="$$(xcrun simctl list devices booted | rg -o -m1 '[0-9A-F-]{36}')"; \
	fi; \
	if [ -z "$$SIM_UDID" ]; then \
		echo "ERROR: no booted iOS Simulator found."; \
		echo "Open Simulator.app and ensure '$(MOBILE_IOS_SIMULATOR)' is booted, then rerun."; \
		exit 1; \
	fi; \
	echo "Installing $$APP_PATH on $$SIM_UDID"; \
	xcrun simctl install "$$SIM_UDID" "$$APP_PATH"; \
	CONFIGURE_URL="$$(./scripts/mobile/build_configure_url.sh 2>/dev/null || true)"; \
	if [ -n "$$CONFIGURE_URL" ]; then \
		echo "Launching $(MOBILE_IOS_BUNDLE_ID) on $$SIM_UDID (with gateway bootstrap)"; \
		xcrun simctl launch "$$SIM_UDID" "$(MOBILE_IOS_BUNDLE_ID)" "$$CONFIGURE_URL"; \
	else \
		echo "Launching $(MOBILE_IOS_BUNDLE_ID) on $$SIM_UDID"; \
		echo "  (no gateway credentials found — skipping auto-configure)"; \
		xcrun simctl launch "$$SIM_UDID" "$(MOBILE_IOS_BUNDLE_ID)"; \
	fi

# Build and install Loom Companion on a connected iPhone.
# Requires: device in dev mode, trusted, and automatic signing configured in Xcode.
# Optional overrides:
#   MOBILE_IOS_CONFIGURATION=Release
#   APPLE_TEAM_ID=XXXXXXXXXX
mobile-app-run-device: mobile-ios-project-sync
	@echo "== Loom Companion → iPhone =="
	@DEVICE_LINE="$$(xcodebuild -project "$(MOBILE_IOS_PROJECT)" \
		-scheme "$(MOBILE_IOS_SCHEME)" \
		-showdestinations 2>/dev/null \
		| grep 'platform:iOS,' | grep -v 'Simulator' | head -1)"; \
	if [ -z "$$DEVICE_LINE" ]; then \
		echo "ERROR: No connected iPhone found."; \
		echo "Connect your iPhone via USB, unlock it, and ensure it is trusted."; \
		exit 1; \
	fi; \
	DEVICE_ID="$$(echo "$$DEVICE_LINE" | sed -n 's/.*id:\([^,}]*\).*/\1/p' | tr -d ' ')"; \
	DEVICE_NAME="$$(echo "$$DEVICE_LINE" | sed -n 's/.*name:\([^,}]*\).*/\1/p' | sed 's/^ *//;s/ *$$//')"; \
	echo "Device: $$DEVICE_NAME ($$DEVICE_ID)"; \
	echo "Building $(MOBILE_IOS_SCHEME) ($(MOBILE_IOS_CONFIGURATION))..."; \
	TEAM_FLAG=""; \
	if [ -n "$${APPLE_TEAM_ID:-}" ]; then \
		TEAM_FLAG="DEVELOPMENT_TEAM=$$APPLE_TEAM_ID"; \
	fi; \
	xcodebuild -project "$(MOBILE_IOS_PROJECT)" \
		-scheme "$(MOBILE_IOS_SCHEME)" \
		-destination "id=$$DEVICE_ID" \
		-configuration "$(MOBILE_IOS_CONFIGURATION)" \
		-derivedDataPath "$(MOBILE_IOS_DERIVED_DATA)" \
		-allowProvisioningUpdates \
		$$TEAM_FLAG \
		build 2>&1 | tail -n 20; \
	BUILD_EXIT=$$?; \
	if [ $$BUILD_EXIT -ne 0 ]; then \
		echo ""; \
		echo "ERROR: Build failed. Full log: /tmp/loom-mobile-app-build.log"; \
		echo "Common fixes:"; \
		echo "  - Set APPLE_TEAM_ID: make mobile-app-run-device APPLE_TEAM_ID=XXXXXXXXXX"; \
		echo "  - Open Xcode and configure signing: make mobile-app-open"; \
		exit 1; \
	fi; \
	echo ""; \
	echo "Installing on $$DEVICE_NAME..."; \
	APP_PATH="$(MOBILE_IOS_DERIVED_DATA)/Build/Products/$(MOBILE_IOS_CONFIGURATION)-iphoneos/$(MOBILE_IOS_APP_NAME).app"; \
	if [ ! -d "$$APP_PATH" ]; then \
		echo "ERROR: app bundle not found at $$APP_PATH"; \
		exit 1; \
	fi; \
	CONFIGURE_URL="$$(./scripts/mobile/build_configure_url.sh 2>/dev/null || true)"; \
	if [ -n "$$CONFIGURE_URL" ]; then \
		echo "Gateway bootstrap URL resolved (will pass as launch argument)."; \
	else \
		echo "No gateway credentials found — skipping auto-configure."; \
		echo "  (set HUD_MOBILE_OPERATOR_TOKEN + CF_ACCESS_CLIENT_ID/SECRET, or ensure kubectl can read loom-hub/loom-secrets)"; \
	fi; \
	ios-deploy --bundle "$$APP_PATH" --id "$$DEVICE_ID" --justlaunch \
		$$( [ -n "$$CONFIGURE_URL" ] && echo "--args '$$CONFIGURE_URL'" ) 2>/dev/null && { \
		echo "Launched $(MOBILE_IOS_BUNDLE_ID) on $$DEVICE_NAME"; \
		exit 0; \
	}; \
	echo "ios-deploy not available, trying devicectl..."; \
	xcrun devicectl device install app --device "$$DEVICE_ID" "$$APP_PATH" 2>&1 && \
	if [ -n "$$CONFIGURE_URL" ]; then \
		xcrun devicectl device process launch --device "$$DEVICE_ID" "$(MOBILE_IOS_BUNDLE_ID)" "$$CONFIGURE_URL" 2>&1; \
	else \
		xcrun devicectl device process launch --device "$$DEVICE_ID" "$(MOBILE_IOS_BUNDLE_ID)" 2>&1; \
	fi && { \
		echo "Launched $(MOBILE_IOS_BUNDLE_ID) on $$DEVICE_NAME"; \
		exit 0; \
	}; \
	echo ""; \
	echo "Build succeeded! App bundle: $$APP_PATH"; \
	echo "Auto-install failed. Install manually:"; \
	echo "  1) Open Xcode: make mobile-app-open"; \
	echo "  2) Select $$DEVICE_NAME as the run destination"; \
	echo "  3) Press Cmd+R to run"

# One-command local mobile dev bootstrap:
# - ensures bin/loom exists
# - generates a fresh mobile operator token
# - restarts HUD with that token
# - opens the iOS app project in Xcode
# - prints copy/paste URL + token values
mobile-dev:
	@./scripts/mobile/dev_bootstrap.sh

# One-command gateway bootstrap:
# - validates gateway surfaces via mobile-gateway-preflight
# - generates a fresh mobile operator token
# - patches loom-hub/loom-secrets (token/scopes and CF token normalization when configured)
# - rollout-restarts deployment/mobile-hud
# - verifies remote /api/mobile/v1/ping and prints copy/paste-ready values
mobile-gateway-dev:
	@./scripts/mobile/gateway_preflight.sh
	@./scripts/mobile/gateway_bootstrap.sh

# Validate signing-related environment and show current Xcode signing state.
mobile-signing-check: mobile-ios-project-sync
	@echo "== Loom Companion Signing Check =="
	@echo "Project: $(MOBILE_IOS_PROJECT)"
	@echo ""
	@for key in APPLE_CERTIFICATE APPLE_CERTIFICATE_PASSWORD APPLE_TEAM_ID APPLE_PROVISIONING_PROFILE APPLE_API_ISSUER APPLE_API_KEY APPLE_API_KEY_BASE64; do \
		val=$$(eval "printf '%s' \"\$${$$key:-}\""); \
		if [ -n "$$val" ]; then \
			echo "PASS: $$key is set"; \
		else \
			echo "WARN: $$key is not set"; \
		fi; \
	done
	@echo ""
	@echo "Resolved Xcode signing fields (Release, generic iOS):"
	@xcodebuild -project "$(MOBILE_IOS_PROJECT)" \
		-scheme "$(MOBILE_IOS_SCHEME)" \
		-configuration Release \
		-destination 'generic/platform=iOS' \
		-showBuildSettings 2>/dev/null | \
		rg 'PRODUCT_BUNDLE_IDENTIFIER|CODE_SIGN_STYLE|CODE_SIGN_IDENTITY|DEVELOPMENT_TEAM|PROVISIONING_PROFILE_SPECIFIER' || true

# Import Apple certificate/profile into temporary keychain and emit build.env.
mobile-signing-prepare:
	@chmod +x scripts/mobile/import-certificate.sh
	@./scripts/mobile/import-certificate.sh

# Cleanup temporary keychain created by mobile-signing-prepare.
mobile-signing-cleanup:
	@chmod +x scripts/mobile/cleanup-signing.sh
	@./scripts/mobile/cleanup-signing.sh

# Archive and export an app-store IPA using manual signing inputs.
mobile-app-archive-export:
	@chmod +x scripts/mobile/archive_export.sh
	@./scripts/mobile/archive_export.sh

# Launch HUD for LAN iPhone testing (requires mobile auth token).
mobile-hud:
	@if [ ! -x "./bin/loom" ]; then \
		echo "bin/loom not found; building with LOOM_BUILD_P=$(LOOM_BUILD_P) CGO_ENABLED=$(CGO_ENABLED)"; \
		if ! $(MAKE) loom LOOM_BUILD_P=$(LOOM_BUILD_P) CGO_ENABLED=$(CGO_ENABLED); then \
			echo "ERROR: failed to build bin/loom (likely memory pressure)."; \
			echo "Try closing Simulator/Xcode and rerun: LOOM_BUILD_P=1 CGO_ENABLED=0 make loom"; \
			exit 1; \
		fi; \
	fi
	@if [ "$${MOBILE_HUD_REBUILD:-0}" = "1" ]; then \
		echo "MOBILE_HUD_REBUILD=1; rebuilding loom with LOOM_BUILD_P=$(LOOM_BUILD_P) CGO_ENABLED=$(CGO_ENABLED)"; \
		if ! $(MAKE) loom LOOM_BUILD_P=$(LOOM_BUILD_P) CGO_ENABLED=$(CGO_ENABLED); then \
			if [ -x "./bin/loom" ]; then \
				echo "WARN: rebuild failed (likely memory pressure); using existing ./bin/loom"; \
			else \
				echo "ERROR: rebuild failed and no existing ./bin/loom is available."; \
				exit 1; \
			fi; \
		fi; \
	fi
	@if [ -z "$$HUD_MOBILE_OPERATOR_TOKEN" ]; then \
		echo "ERROR: HUD_MOBILE_OPERATOR_TOKEN is required."; \
		echo "Set one with: export HUD_MOBILE_OPERATOR_TOKEN=\"$$(openssl rand -hex 32)\""; \
		exit 1; \
	fi
	@SCOPES=$${HUD_MOBILE_OPERATOR_SCOPES:-mobile:read,mobile:session:create,mobile:session:end,mobile:push}; \
	echo "Launching HUD for mobile testing on http://0.0.0.0:3333"; \
	echo "Scopes: $$SCOPES"; \
	./bin/loom hud --bind 0.0.0.0 --port 3333 \
		--mobile-operator-token "$$HUD_MOBILE_OPERATOR_TOKEN" \
		--mobile-operator-scopes "$$SCOPES"

# =============================================================================
# CHANGELOG TARGETS - Autogenerated from conventional commits via py-changelog-ai
# =============================================================================

CHANGELOG_AI := changelog-ai
CHANGELOG_AI_LIB := $(WORKSPACE_ROOT)/libs/py-changelog-ai

# Generate CHANGELOG.generated.md (Keep-a-Changelog format) from full git history.
# The hand-curated CHANGELOG.md remains the source of truth for releases;
# this target produces a machine-generated companion for cross-reference.
changelog:
	@if ! command -v $(CHANGELOG_AI) >/dev/null 2>&1; then \
		echo "Installing changelog-ai from $(CHANGELOG_AI_LIB)..."; \
		pip install -e "$(CHANGELOG_AI_LIB)" >/dev/null 2>&1; \
	fi
	$(CHANGELOG_AI) generate --full -o CHANGELOG.generated.md --config .changelog-ai.yaml --summary
	@echo "✓ Generated CHANGELOG.generated.md"

# Generate HTML changelog with Aurora theme.
changelog-html:
	@if ! command -v $(CHANGELOG_AI) >/dev/null 2>&1; then \
		echo "Installing changelog-ai from $(CHANGELOG_AI_LIB)..."; \
		pip install -e "$(CHANGELOG_AI_LIB)" >/dev/null 2>&1; \
	fi
	$(CHANGELOG_AI) generate --full -o CHANGELOG.html -f html --theme aurora --config .changelog-ai.yaml
	@echo "✓ Generated CHANGELOG.html"

# Generate JSON changelog for programmatic consumption.
changelog-json:
	@if ! command -v $(CHANGELOG_AI) >/dev/null 2>&1; then \
		echo "Installing changelog-ai from $(CHANGELOG_AI_LIB)..."; \
		pip install -e "$(CHANGELOG_AI_LIB)" >/dev/null 2>&1; \
	fi
	$(CHANGELOG_AI) generate --full -o CHANGELOG.json -f json --config .changelog-ai.yaml
	@echo "✓ Generated CHANGELOG.json"

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
	@echo "Context: $(CURDIR)"
	@echo "Named libs context: $(WORKSPACE_ROOT)/libs"
	cd $(CURDIR) && docker build \
		--build-context libs=$(WORKSPACE_ROOT)/libs \
		-t $(CUSTOM_SERVER_IMAGE):$(IMAGE_TAG) \
		-t $(CUSTOM_SERVER_IMAGE):latest \
		-f Dockerfile.custom-server.local .
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

# Full deploy: validate, build, push, update gitops, reconcile
deploy: deploy-check docker-push deploy-update-images deploy-commit deploy-reconcile
	@echo ""
	@echo "✓ Deployment complete!"
	@echo "  Image tag: $(IMAGE_TAG)"
	@echo "  Registry:  $(REGISTRY)"

# Validate the deploy mutation prerequisites before changing GitOps state.
deploy-check: loom
	@echo "Running deploy validation gates..."
	@./bin/loom validate configs
	@./bin/loom validate schemas
	@./bin/loom validate rbac --source repo
	@echo "✓ Deploy validation gates passed"

# Update image tags in Flux Kustomization CRD (single file in gitops)
deploy-update-images:
	@echo "Updating image tags in Flux Kustomization..."
	@if [ ! -f "$(FLUX_KUST)" ]; then \
		echo "ERROR: Flux Kustomization not found: $(FLUX_KUST)"; \
		echo "Set GITOPS_DIR to override"; \
		exit 1; \
	fi
	@python3 scripts/deploy/flux_deploy.py update-images --file "$(FLUX_KUST)" --tag "$(IMAGE_TAG)"
	@echo "✓ Image tags updated to $(IMAGE_TAG)"
	@echo ""
	@echo "Changed files:"
	@cd $(GITOPS_DIR) && git diff --name-only clusters/

# Reconcile Flux (both git sources + kustomizations)
deploy-reconcile:
	@echo "Reconciling Flux..."
	@if command -v flux >/dev/null 2>&1; then \
		flux reconcile source git loom-core -n flux-system && \
		flux reconcile source git gitops-gitlab -n flux-system && \
		flux reconcile kustomization apps -n flux-system && \
		flux reconcile kustomization loom-hub-servers -n flux-system; \
	else \
		echo "Warning: flux CLI not found, skipping reconcile"; \
		echo "Run manually:"; \
		echo "  flux reconcile source git loom-core -n flux-system"; \
		echo "  flux reconcile source git gitops-gitlab -n flux-system"; \
		echo "  flux reconcile kustomization apps -n flux-system"; \
		echo "  flux reconcile kustomization loom-hub-servers -n flux-system"; \
	fi

# Commit gitops changes
deploy-commit:
	@echo "Committing gitops changes..."
	@cd $(GITOPS_DIR) && \
		git add clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml && \
		git commit -m "chore(loom-hub): update images to $(IMAGE_TAG)" && \
		git push
	@echo "✓ GitOps changes committed and pushed"

# Show deployment status
deploy-status:
	@python3 scripts/deploy/flux_deploy.py status --file "$(FLUX_KUST)" --tag "$(IMAGE_TAG)" --registry "$(REGISTRY)" --namespace loom-hub --flux-namespace flux-system
