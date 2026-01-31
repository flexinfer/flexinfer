---
title: OCI Registry Model Caching
description: Using OCI registries (Harbor, GHCR, ECR) as model sources
---

# OCI Registry Model Caching

FlexInfer supports OCI (Open Container Initiative) registries as model sources. This allows you to store and distribute ML models as OCI artifacts, benefiting from container registry infrastructure like Harbor, GHCR, ECR, or any OCI-compliant registry.

## Overview

OCI model caching provides several advantages:

| Feature | HuggingFace | OCI Registry |
|---------|-------------|--------------|
| Air-gapped support | No | Yes |
| Version control | Git LFS | Tags & digests |
| Access control | HF tokens | Docker auth |
| Private hosting | HF Enterprise | Any registry |
| Bandwidth costs | HF egress | Self-hosted |
| Content signing | Limited | Cosign/Notary |

## Source Formats

FlexInfer recognizes two OCI source prefixes:

```yaml
# OCI with tag
source: oci://registry.example.com/models/llama3:v1.0

# OCI with digest (immutable reference)
source: oras://registry.example.com/models/llama3@sha256:abc123...

# Harbor with project path
source: oci://harbor.lan/library/models/qwen3-8b:q4f16_1
```

Both `oci://` and `oras://` prefixes are supported and behave identically.

## Authentication

### Docker Config Secret

For private registries, create a Kubernetes secret with Docker credentials:

```bash
# Create docker config secret
kubectl create secret docker-registry harbor-credentials \
  --namespace flexinfer-system \
  --docker-server=harbor.lan \
  --docker-username=admin \
  --docker-password=Harbor12345 \
  --docker-email=admin@example.com
```

Or create from existing Docker config:

```bash
kubectl create secret generic harbor-credentials \
  --namespace flexinfer-system \
  --from-file=.dockerconfigjson=$HOME/.docker/config.json \
  --type=kubernetes.io/dockerconfigjson
```

### Reference in ModelCache

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-mlc
  namespace: flexinfer-system
spec:
  source: oci://harbor.lan/library/qwen3-8b-mlc:q4f16_1
  ociRegistrySecretRef: harbor-credentials
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 10Gi
```

## Packaging Models as OCI Artifacts

Use ORAS (OCI Registry As Storage) to push models to your registry.

### Install ORAS

```bash
# macOS
brew install oras

# Linux
curl -LO https://github.com/oras-project/oras/releases/download/v1.2.2/oras_1.2.2_linux_amd64.tar.gz
tar -xzf oras_1.2.2_linux_amd64.tar.gz
sudo mv oras /usr/local/bin/
```

### Push a Model

```bash
# Login to registry
oras login harbor.lan -u admin -p Harbor12345

# Download model from HuggingFace
huggingface-cli download mlc-ai/Qwen3-8B-q4f16_1-MLC --local-dir ./qwen3-8b-mlc

# Push to OCI registry
cd qwen3-8b-mlc
oras push harbor.lan/library/qwen3-8b-mlc:q4f16_1 .

# With specific artifact type
oras push harbor.lan/library/qwen3-8b-mlc:q4f16_1 \
  --artifact-type application/vnd.flexinfer.model.v1 \
  .
```

### Tagging Strategy

Recommended tagging conventions:

```
<model-name>:<quantization>              # e.g., qwen3-8b:q4f16_1
<model-name>:<quantization>-<version>    # e.g., qwen3-8b:q4f16_1-v2
<model-name>:latest                      # mutable, for testing
<model-name>@sha256:...                  # immutable, for production
```

## FlexInfer Configuration

### Basic OCI ModelCache

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama3-8b-oci
  namespace: flexinfer-system
spec:
  source: oci://harbor.lan/library/llama3-8b-instruct:q4f16_1
  ociRegistrySecretRef: harbor-credentials
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 10Gi
```

### With Immutable Digest

For production, use digests to ensure immutable references:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama3-8b-prod
  namespace: flexinfer-system
spec:
  # Immutable reference via digest
  source: oci://harbor.lan/library/llama3-8b-instruct@sha256:a1b2c3d4e5f6...
  ociRegistrySecretRef: harbor-credentials
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 10Gi
```

### Memory Strategy with OCI

For fastest access, cache OCI models in RAM:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-fast
  namespace: flexinfer-system
spec:
  source: oci://harbor.lan/library/qwen3-8b-mlc:q4f16_1
  ociRegistrySecretRef: harbor-credentials
  storageStrategy: Memory
  evictionPolicy: LRU
  evictionThresholdPercent: 85
  retentionPriority: 80
```

## Complete Example

```yaml
# 1. Create registry secret
apiVersion: v1
kind: Secret
metadata:
  name: harbor-credentials
  namespace: flexinfer-system
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64-encoded-docker-config>
---
# 2. Create ModelCache from OCI registry
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: mistral-7b-oci
  namespace: flexinfer-system
spec:
  source: oci://harbor.lan/library/mistral-7b-instruct:q4f16_1
  ociRegistrySecretRef: harbor-credentials
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 8Gi
---
# 3. Create ModelDeployment referencing the cache
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: mistral-7b
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  model: Mistral-7B-Instruct-v0.3
  modelCacheRef: mistral-7b-oci

  mlcllm:
    mode: local
    overrides:
      maxTotalSeqLength: 8192

  resources:
    limits:
      nvidia.com/gpu: 1
      memory: 16Gi

  minReplicas: 0
  replicas: 1
  idleTimeoutSeconds: 600
```

## Status Fields

When using OCI sources, the ModelCache status includes additional fields:

```yaml
status:
  phase: Ready
  path: mistral-7b-oci:/mistral-7b-oci
  # OCI-specific fields
  ociDigest: sha256:a1b2c3d4e5f6...
  ociPulledAt: "2025-01-30T15:30:00Z"
  cacheSizeBytes: 4500000000
```

## Air-Gapped Deployments

For air-gapped environments:

1. **Mirror models to internal registry:**
   ```bash
   # On internet-connected machine
   oras pull harbor.external.com/models/llama3:latest -o ./llama3

   # Transfer files to air-gapped network

   # On air-gapped machine
   oras push harbor.internal.lan/models/llama3:latest ./llama3
   ```

2. **Configure FlexInfer to use internal registry:**
   ```yaml
   spec:
     source: oci://harbor.internal.lan/models/llama3:latest
     ociRegistrySecretRef: internal-registry-credentials
   ```

## Harbor-Specific Setup

### Robot Account (Recommended)

Create a Harbor robot account for FlexInfer:

1. Go to Harbor UI > Project > Robot Accounts
2. Create robot with `pull` permission on model repositories
3. Use robot credentials in Kubernetes secret:
   ```bash
   kubectl create secret docker-registry harbor-robot \
     --docker-server=harbor.lan \
     --docker-username='robot$flexinfer' \
     --docker-password=<robot-token>
   ```

### Project Quotas

Configure project quotas in Harbor to prevent storage exhaustion:
- Set storage quota per project
- Enable garbage collection for unused blobs
- Monitor with Harbor metrics

## Troubleshooting

### "unauthorized: authentication required"

**Cause:** Invalid or missing registry credentials.

**Solution:**
1. Verify secret exists: `kubectl get secret harbor-credentials -n flexinfer-system`
2. Check secret is referenced: `spec.ociRegistrySecretRef: harbor-credentials`
3. Verify credentials: `oras login harbor.lan -u <user> -p <pass>`

### "manifest unknown"

**Cause:** Model tag doesn't exist in registry.

**Solution:**
1. Verify artifact exists: `oras manifest fetch harbor.lan/library/model:tag`
2. Check exact tag/digest spelling
3. Ensure model was pushed successfully

### Download Job Stuck

**Cause:** Network issues or large model size.

**Solution:**
1. Check job logs: `kubectl logs job/<cache-name>-downloader -n flexinfer-system`
2. Verify network connectivity to registry
3. Increase job timeout if needed

### Certificate Errors

**Cause:** Self-signed or untrusted registry certificate.

**Solution:** Add CA certificate to cluster:
```yaml
# In ORAS container, mount CA bundle
volumes:
  - name: ca-certs
    configMap:
      name: harbor-ca-cert
volumeMounts:
  - name: ca-certs
    mountPath: /etc/ssl/certs/harbor-ca.crt
    subPath: ca.crt
```

## Best Practices

1. **Use digests for production** - Tags can be overwritten, digests are immutable
2. **Set retention priorities** - Higher priority for production models
3. **Monitor registry storage** - Enable garbage collection in Harbor
4. **Sign artifacts** - Use Cosign for supply chain security
5. **Replicate across registries** - Harbor supports replication for DR

## References

- [ORAS Documentation](https://oras.land/docs/)
- [Harbor Documentation](https://goharbor.io/docs/)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)
- [FlexInfer Caching Guide](./caching.md)
