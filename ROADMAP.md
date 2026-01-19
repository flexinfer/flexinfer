# Project Roadmap

> Last Updated: January 2026

## Current Status

FlexInfer is **functional and working** with comprehensive implementations of all core components. The project is ready for deployment but needs deployment tooling to make it accessible to end users.

### Implemented Features
- ✅ **Node Agent**: Hardware detection and labeling system
- ✅ **Controller Manager**: Complete CRD reconciliation with status management
- ✅ **Scheduler Extender**: Advanced filtering and scoring algorithms
- ✅ **Benchmarker**: Model performance measurement framework
- ✅ **API Types**: Comprehensive CRD definitions with status tracking
- ✅ **Test Suite**: Extensive unit tests across all components
- ✅ **Model Caching**: Intelligent model artifact management with deduplication
- ✅ **Resource Management**: Complete lifecycle management of AI workload deployments

## Upcoming Work

### High Priority (Deployment Ready)
- [x] **Complete Helm templates** - Finish charts/flexinfer/ with proper configurations
- [x] **Helm security templates** - NetworkPolicy and PodDisruptionBudget templates
- [ ] **Installation documentation** - Step-by-step deployment guides
- [ ] **Integration tests** - End-to-end testing scenarios
- [x] **Real benchmarking** - Real inference benchmarking (Ollama, vLLM, MLC-LLM, llama.cpp)
- [x] **GPUGroup exclusive scheduling** - Single model per GPU group with demand-based swapping
- [x] **AntiThrashing configuration** - Configurable cooldown periods for model swaps

### Backend-Specific Work
- [ ] **ROCm GFX1100 image builds** - Complete MLC-LLM ROCm 6.4 image with gfx1100 tuning
- [ ] **Maxwell pre-compiled model docs** - Document FP32 model requirements and pre-compilation
- [ ] **CPU backend support** - Add explicit CPU-only inference via llama.cpp
- [ ] **VRAM detection** - Implement real free VRAM detection in node agent

### Medium Priority (Production Ready)
- [x] **Structured logging migration** - Migrate proxy from log.Printf to slog
- [x] **Environment variable documentation** - Complete docs/CONFIGURATION.md
- [ ] **Performance optimization** - Memory usage and startup time improvements
- [ ] **Security hardening** - RBAC refinements and security scanning
- [ ] **Monitoring dashboards** - Grafana dashboards for operational visibility
- [ ] **Documentation** - API documentation and troubleshooting guides

### Low Priority (Advanced Features)
- [ ] **KV-Cache tiering** - Advanced memory management
- [ ] **Harbor OCI integration** - Direct model registry support
- [ ] **Multi-tenancy** - Namespace isolation features
- [ ] **CNCF submission** - Sandbox application preparation

## Innovation Roadmap

- **"Flash-Loader" Sidecar**: P2P/RDMA model loading to bypass disk I/O.
- **Context-Aware Router**: L7 Prefix-Caching router for "Chat with Doc" workloads.
- **Dynamic Multi-LoRA**: Hot-swapping adapters on running deployments.
- **Spot-Instance Resilience**: Proactive draining on termination notice.

## References

| Document | Purpose |
|----------|---------|
| [README.md](README.md) | Project overview and architecture |
| [AGENTS.md](AGENTS.md) | Agent guidance |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development guidelines |
