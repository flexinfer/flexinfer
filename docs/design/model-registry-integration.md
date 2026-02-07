# Model Registry Integration Design

**Status:** Proposed
**Author:** FlexInfer Team
**Created:** 2026-01-31

## Overview

Model registry integration enables FlexInfer to seamlessly discover, download, and cache models from popular registries:

- **HuggingFace Hub**: Largest open-source model repository
- **Ollama Library**: Curated collection of optimized models
- **Private Registries**: OCI-based model storage (Harbor, GHCR, ECR)

## Goals

1. **Discovery**: Browse and search models from the cluster
2. **Metadata**: Expose model size, requirements, and compatibility
3. **Caching**: Smart pre-fetching based on popularity and usage patterns
4. **Versioning**: Track model versions and enable rollbacks
5. **Authentication**: Support private models with credentials

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     FlexInfer Controller                     │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  HuggingFace │  │   Ollama    │  │   OCI Registry      │  │
│  │   Adapter    │  │   Adapter   │  │     Adapter         │  │
│  └──────┬──────┘  └──────┬──────┘  └──────────┬──────────┘  │
│         │                │                     │             │
│         └────────────────┼─────────────────────┘             │
│                          │                                   │
│                 ┌────────▼────────┐                         │
│                 │  Registry Cache  │                         │
│                 │    (In-Memory)   │                         │
│                 └────────┬────────┘                         │
│                          │                                   │
│         ┌────────────────┼────────────────┐                 │
│         │                │                │                 │
│  ┌──────▼──────┐  ┌──────▼──────┐  ┌──────▼──────┐         │
│  │ ModelCatalog │  │ ModelCache  │  │   Model     │         │
│  │  (Discovery) │  │  (Storage)  │  │  (Runtime)  │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
└─────────────────────────────────────────────────────────────┘
```

## API Design

### ModelCatalog CRD

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: ModelCatalog
metadata:
  name: huggingface
spec:
  # Registry type
  type: HuggingFace  # HuggingFace, Ollama, OCI

  # Connection settings
  endpoint: https://huggingface.co

  # Authentication (optional)
  auth:
    secretRef:
      name: hf-token

  # Sync configuration
  sync:
    # Auto-sync interval
    interval: 1h

    # Filter models to sync metadata for
    filter:
      # Only sync models matching these patterns
      include:
        - "meta-llama/*"
        - "mistralai/*"
        - "microsoft/phi-*"
      # Exclude patterns
      exclude:
        - "*-GGUF"  # Already in GGUF format
        - "*-gptq"  # Already quantized

    # Limit number of models to track
    maxModels: 100

status:
  phase: Ready
  lastSync: "2026-01-31T10:00:00Z"
  modelCount: 45
  # Cached model metadata
  models:
    - name: meta-llama/Llama-3-8B-Instruct
      size: 16106127360
      format: safetensors
      license: llama3
      downloads: 1500000
      trending: true
      requirements:
        minVRAM: 16GB
        recommendedVRAM: 24GB
```

### ModelSearch API

New CLI command and API endpoint for searching registries:

```bash
# Search HuggingFace for models
$ flexinfer search "llama 8b" --registry huggingface
NAME                                SIZE    LICENSE   DOWNLOADS   VRAM
meta-llama/Llama-3-8B-Instruct     15GB    llama3    1.5M        16GB
meta-llama/Llama-3.1-8B-Instruct   15GB    llama3.1  2.1M        16GB
NousResearch/Hermes-3-Llama-3.1-8B 15GB    llama3.1  500K        16GB

# Search Ollama library
$ flexinfer search "code" --registry ollama
NAME              SIZE    PARAMS   CONTEXT   DESCRIPTION
codellama:7b      3.8GB   7B       16K       Code Llama base
codellama:13b     7.4GB   13B      16K       Code Llama large
deepseek-coder    776MB   1.3B     16K       DeepSeek Coder
qwen2.5-coder     4.7GB   7B       128K      Qwen 2.5 Coder

# Get model details
$ flexinfer info meta-llama/Llama-3-8B-Instruct
Name:           meta-llama/Llama-3-8B-Instruct
Registry:       HuggingFace
Size:           15GB (FP16 safetensors)
License:        Llama 3 Community
Downloads:      1.5M
Parameters:     8B
Context:        8K tokens
Languages:      en
Tasks:          text-generation, chat

Requirements:
  Minimum VRAM:     16GB
  Recommended VRAM: 24GB
  Backends:         ollama, vllm, mlc-llm, llamacpp

Quantized Versions:
  TheBloke/Llama-3-8B-Instruct-GGUF  (GGUF Q4_K_M, 4.7GB)
  TheBloke/Llama-3-8B-Instruct-AWQ   (AWQ 4bit, 4.5GB)
```

### Quick Deploy from Registry

```bash
# Deploy directly from registry with sensible defaults
$ flexinfer deploy meta-llama/Llama-3-8B-Instruct --name llama3
Model llama3 created from meta-llama/Llama-3-8B-Instruct
  Backend: ollama (auto-selected)
  GPU: nvidia (auto-detected)
  Cache: SharedPVC (auto-created)

Waiting for model to be ready...
Model llama3 is Ready (took 2m30s)
Endpoint: http://flexinfer-proxy.flexinfer-system/v1/chat/completions
```

## Registry Adapters

### HuggingFace Adapter

```go
type HuggingFaceAdapter struct {
    endpoint string
    token    string
    client   *http.Client
}

func (h *HuggingFaceAdapter) Search(query string, opts SearchOptions) ([]ModelInfo, error) {
    // Use HuggingFace Hub API
    // GET https://huggingface.co/api/models?search=query&filter=text-generation
}

func (h *HuggingFaceAdapter) GetModelInfo(modelID string) (*ModelInfo, error) {
    // GET https://huggingface.co/api/models/{model_id}
}

func (h *HuggingFaceAdapter) GetDownloadURL(modelID string, file string) (string, error) {
    // Generate presigned URL for model files
}
```

### Ollama Adapter

```go
type OllamaAdapter struct {
    endpoint string  // https://ollama.com/library
}

func (o *OllamaAdapter) Search(query string, opts SearchOptions) ([]ModelInfo, error) {
    // Parse Ollama library page or use API if available
}

func (o *OllamaAdapter) GetModelInfo(modelID string) (*ModelInfo, error) {
    // Get model manifest from registry.ollama.com
}
```

### OCI Adapter

```go
type OCIAdapter struct {
    registry string  // e.g., ghcr.io, harbor.example.com
    auth     *OCIAuth
}

func (o *OCIAdapter) Search(query string, opts SearchOptions) ([]ModelInfo, error) {
    // Use OCI Distribution API to list repositories
}

func (o *OCIAdapter) PullModel(reference string, destPath string) error {
    // Use ORAS to pull model artifact
}
```

## Model Metadata Schema

```go
type ModelInfo struct {
    // Identity
    ID        string `json:"id"`        // e.g., "meta-llama/Llama-3-8B-Instruct"
    Name      string `json:"name"`      // e.g., "Llama 3 8B Instruct"
    Registry  string `json:"registry"`  // e.g., "huggingface"

    // Versioning
    Version   string `json:"version"`   // e.g., "v1.0.0" or commit SHA
    UpdatedAt time.Time `json:"updatedAt"`

    // Size and Format
    SizeBytes int64  `json:"sizeBytes"`
    Format    string `json:"format"`    // safetensors, gguf, awq, etc.

    // Requirements
    Requirements Requirements `json:"requirements"`

    // Metadata
    License     string   `json:"license"`
    Description string   `json:"description"`
    Tags        []string `json:"tags"`
    Downloads   int64    `json:"downloads"`
    Trending    bool     `json:"trending"`

    // Backend compatibility
    Backends []string `json:"backends"` // ollama, vllm, etc.
}

type Requirements struct {
    MinVRAMMB       int64  `json:"minVramMB"`
    RecommendedVRAM int64  `json:"recommendedVramMB"`
    MinRAMMB        int64  `json:"minRamMB"`
    GPUVendors      []string `json:"gpuVendors"` // nvidia, amd, or empty for any
}
```

## Smart Caching

### Popularity-Based Pre-fetching

Track model usage and pre-cache popular models:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: CachePolicy
metadata:
  name: smart-cache
spec:
  # Auto-cache trending models
  autoCacheTrending: true
  trendingThreshold: 10000  # downloads/week

  # Pre-cache based on local usage
  autoCacheUsed: true
  usageThreshold: 3  # requests in last week

  # Cache limits
  maxCacheSize: 500Gi
  maxModels: 20

  # Eviction policy
  eviction:
    strategy: LRU  # LRU, LFU, Size
    minAge: 24h    # Don't evict recently used
```

### Version Tracking

```yaml
status:
  cachedModels:
    - name: meta-llama/Llama-3-8B-Instruct
      version: "abc123def"  # Git commit SHA
      cachedAt: "2026-01-30T10:00:00Z"
      updateAvailable: true
      latestVersion: "def456abc"
```

## Authentication

### HuggingFace Token

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: hf-token
type: Opaque
stringData:
  token: hf_xxxxxxxxxxxxxxxxxxxx
```

### Private OCI Registry

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: harbor-auth
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64-encoded-docker-config>
```

## Implementation Phases

### Phase 1: HuggingFace Integration

1. Implement HuggingFace adapter with search and info
2. Add `flexinfer search` and `flexinfer info` commands
3. Support HF token authentication

### Phase 2: Ollama Library

1. Implement Ollama adapter
2. Add automatic backend detection (ollama models → ollama backend)
3. Support Ollama model manifests

### Phase 3: ModelCatalog CRD

1. Add ModelCatalog CRD for registry configuration
2. Implement metadata sync controller
3. Add trending and popularity tracking

### Phase 4: Smart Caching

1. Add CachePolicy CRD
2. Implement popularity-based pre-fetching
3. Add version tracking and update notifications

## Metrics

```
# Registry API calls
flexinfer_registry_requests_total{registry,operation}
flexinfer_registry_latency_seconds{registry,operation}

# Model downloads
flexinfer_model_downloads_total{registry,model}
flexinfer_model_download_bytes_total{registry,model}

# Cache hit rate
flexinfer_cache_hits_total{registry}
flexinfer_cache_misses_total{registry}
```

## Open Questions

1. **Rate Limiting**: How to handle HuggingFace rate limits? Implement backoff?

2. **License Validation**: Should we validate model licenses before deployment?

3. **Gated Models**: How to handle HuggingFace gated models (require agreement)?

4. **Local Mirroring**: Should we support mirroring registries for air-gapped environments?

## References

- [HuggingFace Hub API](https://huggingface.co/docs/hub/api)
- [Ollama Model Library](https://ollama.com/library)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)
- [ORAS (OCI Registry As Storage)](https://oras.land/)
