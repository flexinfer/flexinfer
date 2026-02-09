# Project Roadmap

> Last Updated: 2026-02-08

## Current Status

FlexInfer is **production-ready** at 85-95% completion. Phases 1-4 are complete, providing:
- Hardened controller reconciliation with immutable field handling
- Production-grade serverless/activator with OpenAI API compatibility
- KV-cache-aware routing with session affinity and least-loaded strategies
- Comprehensive E2E testing and documentation

### Implemented Features
- ✅ **Node Agent**: Hardware detection and labeling system
- ✅ **Controller Manager**: Complete CRD reconciliation with status management
- ✅ **Scheduler Extender**: Advanced filtering and scoring algorithms
- ✅ **Benchmarker**: Model performance measurement framework
- ✅ **API Types**: Comprehensive CRD definitions with status tracking
- ✅ **Test Suite**: Extensive unit tests across all components
- ✅ **Model Caching**: Intelligent model artifact management with deduplication
- ✅ **Resource Management**: Complete lifecycle management of AI workload deployments
- ✅ **Serverless Proxy**: OpenAI-compatible proxy with scale-to-zero activation
- ✅ **Advanced Routing**: Session affinity, prefix-based, and least-loaded routing

## Completed Phases

### Phase 1: Controller & API Hardening ✅
- [x] Service reconciliation preserves immutable fields (clusterIP)
- [x] Deployment reconciliation handles selector immutability
- [x] Multi-replica placement with anti-affinity and topology spread
- [x] NVIDIA runtime requirements codified
- [x] Status clarity with actionable conditions
- [x] Image pinning guidance documented

### Phase 2: Serverless/Activator Hardening ✅
- [x] OpenAI API compatibility documented
- [x] Streaming (SSE) behavior documented
- [x] Cold start budget configuration
- [x] Concurrency caps during activation
- [x] Activation metrics (10 metric families)

### Phase 3: Routing & Performance ✅
- [x] Session affinity via consistent hashing
- [x] Prefix-based routing (opt-in)
- [x] Endpoint discovery for multi-replica models
- [x] Least-loaded routing (opt-in)
- [x] Routing documentation

### Phase 4: Operational Polish ✅
- [x] E2E test harness (`make test-e2e`)
- [x] INSTALL.md refresh
- [x] User quickstart guide
- [x] GPU/backend quirks runbook
- [x] Documentation index/navigation

## Upcoming Work

### High Priority (Deployment Ready)
- [x] **Complete Helm templates** - Finish charts/flexinfer/ with proper configurations
- [x] **Helm security templates** - NetworkPolicy and PodDisruptionBudget templates
- [x] **Installation documentation** - Step-by-step deployment guides (INSTALL.md)
- [x] **Integration tests** - End-to-end testing scenarios (e2e/)
- [x] **Real benchmarking** - Real inference benchmarking (Ollama, vLLM, MLC-LLM, llama.cpp)
- [x] **GPUGroup exclusive scheduling** - Single model per GPU group with demand-based swapping
- [x] **AntiThrashing configuration** - Configurable cooldown periods for model swaps

### Backend-Specific Work
- [x] **ROCm GFX1100 image builds** - MLC-LLM ROCm 6.4 image with gfx1100 tuning (supplementalGroups fix for non-root GPU access)
- [x] **Maxwell pre-compiled model docs** - Document FP32 model requirements and pre-compilation
- [x] **CPU backend support** - Add explicit CPU-only inference via llama.cpp (v1alpha2 docs + HF GGUF selection via config.ggufFile)
- [x] **VRAM detection** - Implement real free VRAM detection + utilization telemetry in node agent (sysfs fallback + chroot host tooling)

### Medium Priority (Production Ready)
- [x] **Structured logging migration** - Migrate proxy from log.Printf to slog
- [x] **Environment variable documentation** - Complete docs/CONFIGURATION.md
- [x] **Routing optimization** - Session affinity, prefix-based, least-loaded (Phase 3)
- [x] **Security hardening** - RBAC reviewed (controller can list/watch nodes) + reduced SA token mounting for v1alpha2 model/cache pods
- [x] **Monitoring dashboards** - Basic dashboards exist (may need expansion)
- [x] **Documentation** - API docs, operations guide, quickstart complete

### Tech Debt (High Priority)
- [x] **TD-1**: Add error handling to ignored returns - Fixed JSON encode-after-header-sent in proxy `handleModels` and scheduler Filter/Score handlers (pre-marshal pattern)
- [x] **TD-3**: Increase CLI test coverage to 50%+ (now 78.6% for `cmd/flexinfer/commands`)
- [~] **TD-4**: Replace panic with graceful error handling - `MustGet` is never called, safe variants used
- [x] **TD-11**: E2E test names violate RFC 1123 - Fixed with lowercase + "/" replacement
- [x] **TD-12**: GPUGroup v1alpha1 not registered in e2e test scheme - Added to scheme

### Tech Debt (Medium Priority)
- [x] **TD-5**: Create v1alpha1 → v1alpha2 migration guide (docs/migration/v1alpha1-to-v1alpha2.md)
- [~] **TD-6**: Centralize hardcoded URLs/ConfigMap names (backends already support env var overrides)
- [~] **TD-7**: E2E tests for GPU scenarios - 6/7 pass on K3s GPU cluster (AMD 7900XTX). GPUGroup tests (3/3) pass, inference basic+streaming+multi-model (3/4) pass. ColdStart gracefully skips (iGPU not usable with ROCm, no spare discrete GPU)

### Tech Debt (Low Priority)
- [x] **TD-8**: Logging consistency - servers use slog, CLI uses fmt.Print (correct pattern)
- [x] **TD-9**: Deprecated benchmark flag cleanup - added warning for --max-tokens
- [x] **TD-10**: Namespace "default" fallback - added warning when POD_NAMESPACE not set

### Low Priority (Advanced Features)
- [ ] **KV-Cache tiering** - Advanced memory management
- [ ] **Harbor OCI integration** - Direct model registry support
- [ ] **Multi-tenancy** - Namespace isolation features
- [ ] **CNCF submission** - Sandbox application preparation

## Phase 5: Multi-Cluster (Future)

See `docs/design/multi-cluster.md` and `docs/planning/phase-5-multi-cluster.md` for details.

- [ ] Cluster Registry (MVP)
- [ ] Cross-Cluster Model Sync
- [ ] Global Routing
- [ ] Advanced Features (weighted routing, cross-cluster GPU sharing)

## Innovation Roadmap

- **"Flash-Loader" Sidecar**: P2P/RDMA model loading to bypass disk I/O.
- **Context-Aware Router**: L7 Prefix-Caching router for "Chat with Doc" workloads. (Partially complete - prefix routing available)
- **Dynamic Multi-LoRA**: Hot-swapping adapters on running deployments.
- **Spot-Instance Resilience**: Proactive draining on termination notice.

## References

| Document | Purpose |
|----------|---------|
| [README.md](README.md) | Project overview and architecture |
| [AGENTS.md](AGENTS.md) | Agent guidance |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development guidelines |
| [docs/planning/](docs/planning/) | Phase planning documents |
| [docs/design/multi-cluster.md](docs/design/multi-cluster.md) | Multi-cluster design |
