# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: setup
setup: ## Install all development dependencies
	@echo "Setting up development environment..."
	make kustomize controller-gen envtest
	go mod download
	@echo "Setup complete."

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./... -coverprofile cover.out

.PHONY: test-unit
test-unit: ## Run unit tests only (skip integration tests)
	SKIP_ENVTEST=1 go test ./... -short

.PHONY: test-integration
test-integration: envtest ## Run integration tests with proper environment
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./controllers -v

.PHONY: test-gpugroup
test-gpugroup: ## Run GPUGroup-specific tests
	SKIP_ENVTEST=1 go test -v ./controllers/... -run "GPUGroup" -timeout 5m

.PHONY: test-proxy
test-proxy: ## Run proxy tests
	go test -v ./internal/proxy/... -timeout 2m

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	SKIP_ENVTEST=1 go test ./... -short -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | tail -1
	@echo "HTML coverage report: go tool cover -html=coverage.out"

##@ E2E Tests

.PHONY: test-e2e
test-e2e: ## Run E2E tests against a real cluster (requires kubeconfig)
	go test -v ./e2e/... -timeout 10m

.PHONY: test-e2e-skip
test-e2e-skip: ## Run E2E tests, skip if no cluster available (CI-friendly)
	go test -v ./e2e/... -timeout 10m -args -skip-no-cluster

.PHONY: test-e2e-namespace
test-e2e-namespace: ## Run E2E tests in a custom namespace (use NAMESPACE=myns)
	go test -v ./e2e/... -timeout 10m -args -namespace=$(NAMESPACE)

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/flexinfer-manager/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/flexinfer-manager/main.go

.PHONY: build-cli
build-cli: ## Build the flexinfer CLI binary.
	go build -o bin/flexinfer ./cmd/flexinfer

.PHONY: install-cli
install-cli: build-cli ## Install the flexinfer CLI to /usr/local/bin.
	cp bin/flexinfer /usr/local/bin/

.PHONY: build-all
build-all: build build-cli ## Build all binaries (manager + CLI + flash-loader).
	go build -o bin/flexinfer-agent ./cmd/flexinfer-agent
	go build -o bin/flexinfer-bench ./cmd/flexinfer-bench
	go build -o bin/flexinfer-proxy ./cmd/flexinfer-proxy
	go build -o bin/flexinfer-sched ./cmd/flexinfer-sched
	go build -o bin/flexinfer-flash-loader ./cmd/flexinfer-flash-loader

.PHONY: build-flash-loader
build-flash-loader: ## Build the flash-loader init container binary.
	go build -o bin/flexinfer-flash-loader ./cmd/flexinfer-flash-loader

.PHONY: test-race
test-race: ## Run tests with race detector
	go test -race ./... -short

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KUSTOMIZE_VERSION ?= v3.8.7
CONTROLLER_TOOLS_VERSION ?= latest
ENVTEST_K8S_VERSION = 1.28.x

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary. If wrong version is installed, it will be removed before downloading.
$(KUSTOMIZE): $(LOCALBIN)
	@if test -x $(LOCALBIN)/kustomize && ! $(LOCALBIN)/kustomize version | grep -q $(KUSTOMIZE_VERSION); then \
		echo "$(LOCALBIN)/kustomize version is not expected $(KUSTOMIZE_VERSION). Removing it before installing."; \
		rm -rf $(LOCALBIN)/kustomize; \
	fi
	test -s $(LOCALBIN)/kustomize || { curl -Ss $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

##@ Backend Images

HARBOR_REGISTRY ?= registry.harbor.lan
MLC_ROCM64_IMAGE ?= $(HARBOR_REGISTRY)/library/mlc-llm:rocm64-src
MLC_MAXWELL_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/mlc-llm:cuda-maxwell-v7

# Docker context for GPU builds (requires remote builder with GPU access)
DOCKER_CONTEXT_GPU ?= 7900xtx

.PHONY: build-mlc-rocm64
build-mlc-rocm64: ## Build MLC-LLM ROCm 6.4 image on GPU node (gfx1100, ~3 hours)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.mlc-rocm64-full -t $(MLC_ROCM64_IMAGE) build/

.PHONY: push-mlc-rocm64
push-mlc-rocm64: ## Push MLC-LLM ROCm 6.4 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(MLC_ROCM64_IMAGE)

.PHONY: build-mlc-maxwell
build-mlc-maxwell: ## Build MLC-LLM Maxwell image on GPU node (sm_52, ~2 hours)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.mlc-cuda-maxwell -t $(MLC_MAXWELL_IMAGE) build/

.PHONY: push-mlc-maxwell
push-mlc-maxwell: ## Push MLC-LLM Maxwell image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(MLC_MAXWELL_IMAGE)

.PHONY: verify-images
verify-images: ## Verify all backend images exist in Harbor registry
	@./scripts/verify-images.sh
