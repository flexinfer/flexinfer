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
	go build -o bin/flexinfer-global-proxy ./cmd/flexinfer-global-proxy
	go build -o bin/flexinfer-sched ./cmd/flexinfer-sched
	go build -o bin/flexinfer-flash-loader ./cmd/flexinfer-flash-loader

.PHONY: build-flash-loader
build-flash-loader: ## Build the flash-loader init container binary.
	go build -o bin/flexinfer-flash-loader ./cmd/flexinfer-flash-loader

.PHONY: build-global-proxy
build-global-proxy: ## Build the global proxy binary.
	go build -o bin/flexinfer-global-proxy ./cmd/flexinfer-global-proxy

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

##@ Runtime Images (config-driven, see build/runtime.yaml)

RUNTIME_PROFILES := gfx1100 gfx906 cuda-maxwell

.PHONY: build-runtime-%
build-runtime-%: ## Build runtime image for a profile (e.g. make build-runtime-gfx1100)
	./build/build-runtime.sh $*

.PHONY: push-runtime-%
push-runtime-%: ## Build + push runtime image for a profile
	./build/build-runtime.sh $* --push

.PHONY: build-runtimes
build-runtimes: ## Build all runtime images sequentially
	./build/build-runtime.sh all

.PHONY: push-runtimes
push-runtimes: ## Build + push all runtime images
	./build/build-runtime.sh all --push

.PHONY: dry-run-runtime-%
dry-run-runtime-%: ## Print docker build command for a profile without executing
	./build/build-runtime.sh $* --dry-run

##@ Legacy Backend Images

HARBOR_REGISTRY ?= registry.harbor.lan
MLC_ROCM64_IMAGE ?= $(HARBOR_REGISTRY)/library/mlc-llm:rocm64-src
MLC_GFX906_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/mlc-llm:rocm64-gfx906
MLC_MAXWELL_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/mlc-llm:cuda-maxwell-v7
VLLM_GFX1100_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/vllm:rocm-gfx1100
VLLM_GFX1100_FA_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/vllm:rocm-gfx1100-fa
VLLM_GFX906_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/vllm:rocm-gfx906
VLLM_GFX906_FA_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/vllm:rocm-gfx906-fa
VLLM_GFX1100_NIGHTLY_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/vllm:rocm-gfx1100-nightly
VLLM_OMNI_GFX1100_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/vllm-omni:rocm-gfx1100
LLAMACPP_GFX1100_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/llamacpp:rocm-gfx1100
LLAMACPP_GFX906_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/llamacpp:rocm-gfx906
LLAMACPP_MAXWELL_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/llamacpp:cuda-maxwell
OLLAMA_MAXWELL_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/ollama:cuda-maxwell
DIFFUSERS_ROCM_IMAGE ?= $(HARBOR_REGISTRY)/library/diffusers-api:rocm-$(shell git rev-parse --short HEAD)
DIFFUSERS_GFX1100_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/diffusers:rocm-gfx1100
DIFFUSERS_GFX906_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/diffusers:rocm-gfx906
DIFFUSERS_CUDA_IMAGE ?= $(HARBOR_REGISTRY)/flexinfer/diffusers:cuda-maxwell

# Docker context for GPU builds (requires remote builder with GPU access)
DOCKER_CONTEXT_GPU ?= 7900xtx

.PHONY: build-mlc-rocm64
build-mlc-rocm64: ## Build MLC-LLM ROCm 6.4 image on GPU node (gfx1100, ~3 hours)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.mlc-rocm64-full -t $(MLC_ROCM64_IMAGE) build/

.PHONY: push-mlc-rocm64
push-mlc-rocm64: ## Push MLC-LLM ROCm 6.4 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(MLC_ROCM64_IMAGE)

.PHONY: build-mlc-gfx906
build-mlc-gfx906: ## Build MLC-LLM gfx906 image on GPU node (~3 hours)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.mlc-rocm64-gfx906 -t $(MLC_GFX906_IMAGE) build/

.PHONY: push-mlc-gfx906
push-mlc-gfx906: ## Push MLC-LLM gfx906 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(MLC_GFX906_IMAGE)

.PHONY: build-vllm-gfx1100
build-vllm-gfx1100: ## Build vLLM gfx1100 image (no FA)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.vllm-rocm-gfx1100 -t $(VLLM_GFX1100_IMAGE) .

.PHONY: push-vllm-gfx1100
push-vllm-gfx1100: ## Push vLLM gfx1100 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(VLLM_GFX1100_IMAGE)

.PHONY: build-vllm-gfx1100-fa
build-vllm-gfx1100-fa: ## Build vLLM gfx1100 flash attention image (prebuilt Navi base, vLLM 0.14.0)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.vllm-rocm-gfx1100-fa -t $(VLLM_GFX1100_FA_IMAGE) .

.PHONY: push-vllm-gfx1100-fa
push-vllm-gfx1100-fa: ## Push vLLM gfx1100 FA image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(VLLM_GFX1100_FA_IMAGE)

.PHONY: build-vllm-gfx1100-nightly
build-vllm-gfx1100-nightly: ## Build vLLM nightly gfx1100 image (from-source, Qwen3.5 + AWQ, ~30-60 min)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.vllm-nightly-rocm-gfx1100 -t $(VLLM_GFX1100_NIGHTLY_IMAGE) .

.PHONY: push-vllm-gfx1100-nightly
push-vllm-gfx1100-nightly: ## Push vLLM nightly gfx1100 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(VLLM_GFX1100_NIGHTLY_IMAGE)

.PHONY: build-vllm-gfx906
build-vllm-gfx906: ## Build vLLM gfx906 image on GPU node
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.vllm-rocm-gfx906 -t $(VLLM_GFX906_IMAGE) build/

.PHONY: push-vllm-gfx906
push-vllm-gfx906: ## Push vLLM gfx906 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(VLLM_GFX906_IMAGE)

.PHONY: build-vllm-gfx906-fa
build-vllm-gfx906-fa: ## Build vLLM gfx906 flash attention image (DEPRECATED — see Dockerfile header)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.vllm-rocm-gfx906-fa -t $(VLLM_GFX906_FA_IMAGE) .

.PHONY: push-vllm-gfx906-fa
push-vllm-gfx906-fa: ## Push vLLM gfx906 FA image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(VLLM_GFX906_FA_IMAGE)

.PHONY: build-vllm-omni-gfx1100
build-vllm-omni-gfx1100: ## Build vLLM-Omni gfx1100 image (multimodal generation, Navi base + pip)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.vllm-omni-rocm-gfx1100 -t $(VLLM_OMNI_GFX1100_IMAGE) .

.PHONY: push-vllm-omni-gfx1100
push-vllm-omni-gfx1100: ## Push vLLM-Omni gfx1100 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(VLLM_OMNI_GFX1100_IMAGE)

.PHONY: build-llamacpp-gfx1100
build-llamacpp-gfx1100: ## Build llama.cpp gfx1100 image (ROCm, RX 7900 series)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.llamacpp-rocm-gfx1100 -t $(LLAMACPP_GFX1100_IMAGE) .

.PHONY: push-llamacpp-gfx1100
push-llamacpp-gfx1100: ## Push llama.cpp gfx1100 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(LLAMACPP_GFX1100_IMAGE)

.PHONY: build-llamacpp-gfx906
build-llamacpp-gfx906: ## Build llama.cpp gfx906 image (ROCm, Radeon VII / Vega20)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.llamacpp-rocm-gfx906 -t $(LLAMACPP_GFX906_IMAGE) build/

.PHONY: push-llamacpp-gfx906
push-llamacpp-gfx906: ## Push llama.cpp gfx906 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(LLAMACPP_GFX906_IMAGE)

.PHONY: build-llamacpp-maxwell
build-llamacpp-maxwell: ## Build llama.cpp Maxwell image (CUDA 11.8, sm_52)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.llamacpp-cuda-maxwell -t $(LLAMACPP_MAXWELL_IMAGE) .

.PHONY: push-llamacpp-maxwell
push-llamacpp-maxwell: ## Push llama.cpp Maxwell image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(LLAMACPP_MAXWELL_IMAGE)

.PHONY: build-mlc-maxwell
build-mlc-maxwell: ## Build MLC-LLM Maxwell image on GPU node (sm_52, ~2 hours)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.mlc-cuda-maxwell -t $(MLC_MAXWELL_IMAGE) build/

.PHONY: push-mlc-maxwell
push-mlc-maxwell: ## Push MLC-LLM Maxwell image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(MLC_MAXWELL_IMAGE)

.PHONY: build-ollama-maxwell
build-ollama-maxwell: ## Build Ollama Maxwell image (CUDA 11.8, sm_52)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.ollama-cuda-maxwell -t $(OLLAMA_MAXWELL_IMAGE) .

.PHONY: push-ollama-maxwell
push-ollama-maxwell: ## Push Ollama Maxwell image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(OLLAMA_MAXWELL_IMAGE)

.PHONY: build-diffusers-rocm
build-diffusers-rocm: ## Build Diffusers ROCm image on GPU node (gfx1100)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.diffusers-rocm -t $(DIFFUSERS_ROCM_IMAGE) .

.PHONY: push-diffusers-rocm
push-diffusers-rocm: ## Push Diffusers ROCm image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(DIFFUSERS_ROCM_IMAGE)

.PHONY: build-diffusers-gfx1100
build-diffusers-gfx1100: ## Build Diffusers ROCm image for gfx1100 (architecture-specific)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.diffusers-rocm-gfx1100 -t $(DIFFUSERS_GFX1100_IMAGE) .

.PHONY: push-diffusers-gfx1100
push-diffusers-gfx1100: ## Push Diffusers ROCm gfx1100 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(DIFFUSERS_GFX1100_IMAGE)

.PHONY: build-diffusers-gfx906
build-diffusers-gfx906: ## Build Diffusers ROCm image for gfx906 (Radeon VII, bitsandbytes from source)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.diffusers-rocm-gfx906 -t $(DIFFUSERS_GFX906_IMAGE) .

.PHONY: push-diffusers-gfx906
push-diffusers-gfx906: ## Push Diffusers ROCm gfx906 image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(DIFFUSERS_GFX906_IMAGE)

.PHONY: build-diffusers-cuda
build-diffusers-cuda: ## Build Diffusers CUDA image for Maxwell (sm_52, CUDA 11.8)
	docker --context $(DOCKER_CONTEXT_GPU) build -f build/Dockerfile.diffusers-cuda -t $(DIFFUSERS_CUDA_IMAGE) .

.PHONY: push-diffusers-cuda
push-diffusers-cuda: ## Push Diffusers CUDA Maxwell image to Harbor
	docker --context $(DOCKER_CONTEXT_GPU) push $(DIFFUSERS_CUDA_IMAGE)

.PHONY: verify-images
verify-images: ## Verify all backend images exist in Harbor registry
	@./scripts/verify-images.sh

##@ Benchmarks

.PHONY: bench-swap
bench-swap: ## Run GPU swap benchmark for shared image generation group
	@./scripts/bench-image-swap.sh $(BENCH_PHASE)

.PHONY: bench-model
bench-model: ## Run LLM model benchmark (MODEL=name ENDPOINT=url)
	@./scripts/bench-model.sh $(BENCH_PHASE)
