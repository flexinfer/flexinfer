# OCI Model Cache Examples

These examples demonstrate using OCI registries (like Harbor) as model sources for FlexInfer.

## Examples

| File | Description |
|------|-------------|
| `harbor-basic.yaml` | Basic setup with Harbor registry |
| `harbor-production.yaml` | Production setup with immutable digests and robot accounts |

## Prerequisites

### 1. Push Models to Registry

Download a model and push it to your OCI registry:

```bash
# Install ORAS
brew install oras  # macOS
# or: apt install oras  # Linux

# Login to registry
oras login harbor.lan -u admin -p Harbor12345

# Download model from HuggingFace
huggingface-cli download mlc-ai/Qwen3-8B-q4f16_1-MLC --local-dir ./qwen3-8b-mlc

# Push to Harbor
cd qwen3-8b-mlc
oras push harbor.lan/library/qwen3-8b-mlc:q4f16_1 .
```

### 2. Create Registry Credentials

```bash
kubectl create secret docker-registry harbor-credentials \
  --namespace flexinfer-system \
  --docker-server=harbor.lan \
  --docker-username=admin \
  --docker-password=Harbor12345
```

### 3. Apply Examples

```bash
# Basic example
kubectl apply -f harbor-basic.yaml

# Watch cache status
kubectl get modelcache -n flexinfer-system -w
```

## Verification

Check the ModelCache status:

```bash
kubectl get modelcache qwen3-8b-harbor -n flexinfer-system -o yaml
```

Expected status when ready:

```yaml
status:
  phase: Ready
  path: qwen3-8b-harbor:/qwen3-8b-harbor
  ociDigest: sha256:...
  ociPulledAt: "2025-01-30T15:30:00Z"
```

## Troubleshooting

### Check Downloader Job

```bash
kubectl get jobs -n flexinfer-system
kubectl logs job/qwen3-8b-harbor-downloader -n flexinfer-system
```

### Verify Registry Access

```bash
# Test from local machine
oras manifest fetch harbor.lan/library/qwen3-8b-mlc:q4f16_1

# Test from cluster
kubectl run oras-test --rm -it --restart=Never \
  --image=ghcr.io/oras-project/oras:v1.2.2 \
  -- login harbor.lan -u admin -p Harbor12345
```

## See Also

- [OCI Caching Guide](../../docs/user/caching-oci.md)
- [FlexInfer Caching Overview](../../docs/user/caching.md)
