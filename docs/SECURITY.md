# FlexInfer Security Guide

This document describes the security architecture of FlexInfer, including RBAC permissions, network policies, and secrets management.

## Overview

FlexInfer follows the principle of least privilege. Each component runs with its own ServiceAccount and minimal permissions required for its function.

## Components and Permissions

### Controller

The controller manages FlexInfer custom resources and underlying Kubernetes resources.

**ServiceAccount:** `flexinfer-controller`

**ClusterRole permissions:**

| API Group | Resources | Verbs | Justification |
|-----------|-----------|-------|---------------|
| ai.flexinfer | modeldeployments, modelcaches, gpugroups, models (+ /status, /finalizers) | get, list, watch, create, update, patch, delete | Primary resources managed by controller |
| apps | deployments, daemonsets | get, list, watch, create, update, patch, delete | Creates backend pods |
| "" (core) | services, pvcs, configmaps, events | get, list, watch, create, update, patch, delete | Supporting resources for models |
| batch | jobs | get, list, watch, create, update, patch, delete | Benchmarking and downloader jobs |
| coordination.k8s.io | leases | get, list, watch, create, update, patch, delete | Leader election |

### Agent (DaemonSet)

The agent runs on each node to detect GPU hardware and report metrics.

**ServiceAccount:** `flexinfer-agent`

**ClusterRole permissions:**

| API Group | Resources | Verbs | Justification |
|-----------|-----------|-------|---------------|
| "" (core) | nodes | get, list, watch, update, patch | Updates node labels with GPU info |

**Host access:**
- Requires access to GPU tools (`nvidia-smi`, `rocm-smi`)
- Reads from `/sys/class/drm/` for AMD GPU sysfs fallback

### Proxy

The proxy routes inference requests to backend pods.

**ServiceAccount:** `flexinfer-proxy`

**Role permissions (namespace-scoped):**

| API Group | Resources | Verbs | Justification |
|-----------|-----------|-------|---------------|
| ai.flexinfer | modeldeployments, gpugroups | get, list, watch, update, patch | Updates lastAccessTime, triggers scale-up |
| ai.flexinfer | models | get, list, watch | Routes requests to models |
| ai.flexinfer | models/status | get, update, patch | Updates model status |
| "" (core) | services | get, list, watch | Discovers backend endpoints |

### Scheduler

The custom scheduler extender optimizes model placement.

**ServiceAccount:** `flexinfer-scheduler`

**ClusterRole permissions:**

| API Group | Resources | Verbs | Justification |
|-----------|-----------|-------|---------------|
| "" (core) | nodes | get, list, watch | Evaluates node resources |
| "" (core) | pods, pods/status | get, list, watch, update, patch, delete | Standard scheduler operations |
| "" (core) | bindings, pods/binding | create | Binds pods to nodes |
| "" (core) | events | create, patch, update | Records scheduling events |
| apps | replicasets, statefulsets | get, list, watch | Evaluates workload topology |
| policy | poddisruptionbudgets | get, list, watch | Respects PDBs |
| storage.k8s.io | storageclasses, csinodes | get, list, watch | Storage-aware scheduling |

### Benchmarker

The benchmarker measures model performance after deployment.

**ServiceAccount:** `flexinfer-benchmarker`

**ClusterRole permissions:**

| API Group | Resources | Verbs | Justification |
|-----------|-----------|-------|---------------|
| "" (core) | nodes | get, list, watch | Reads node GPU capabilities |
| "" (core) | configmaps | get, list, watch, create, update, patch | Stores benchmark results |

## Pod Security

### Security Contexts

All FlexInfer pods use restrictive security contexts:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault

containers:
  - securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
```

**Exceptions:**
- Backend inference pods may need writable filesystem for model caching
- Agent pods need access to GPU tools on the host

### Pod Security Standards

FlexInfer is compatible with Kubernetes Pod Security Standards:

- **Restricted:** Controller, Proxy, Scheduler (with minor adjustments)
- **Baseline:** Agent, Backend pods (due to host access requirements)

## Network Policies

Enable network policies in values.yaml:

```yaml
networkPolicy:
  enabled: true
  prometheusNamespace: monitoring
  defaultDeny: false  # Set to true for strict isolation
```

### Component Network Access

| Component | Ingress From | Egress To |
|-----------|--------------|-----------|
| Controller | Prometheus (metrics) | API server, DNS |
| Proxy | Any namespace (inference), Prometheus | API server, backends, DNS |
| Scheduler | kube-scheduler (extender) | API server, DNS |
| Agent | Prometheus (metrics) | API server, backend metrics, DNS |

### Default Deny Policy

When `networkPolicy.defaultDeny: true`:
- All ingress/egress blocked by default
- Only explicitly allowed traffic permitted
- Recommended for production environments

**Warning:** Default deny may break other workloads in the namespace. Use a dedicated namespace for FlexInfer.

## Secrets Management

### Model Registry Credentials

For private model registries, use Kubernetes secrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: huggingface-token
  namespace: flexinfer-system
type: Opaque
data:
  token: <base64-encoded-token>
```

Reference in ModelCache:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: private-model
spec:
  source:
    type: huggingface
    repository: my-org/private-model
    secretRef:
      name: huggingface-token
      key: token
```

### OCI Registry Credentials

For Harbor or other OCI registries:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: harbor-credentials
  namespace: flexinfer-system
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64-encoded-docker-config>
```

### SOPS + age Encryption

For GitOps workflows, use SOPS with age for secret encryption:

```bash
# Encrypt secrets
sops --encrypt --age age1... secrets.yaml > secrets.enc.yaml

# Flux SOPS integration
flux create kustomization flexinfer-secrets \
  --source=GitRepository/fleet \
  --path=./clusters/production/flexinfer/secrets \
  --decryption-provider=sops \
  --decryption-secret=sops-age
```

## Image Security

### Scanning

All FlexInfer images are scanned with Trivy in CI:

```yaml
# .gitlab-ci.yml
trivy_scan:
  script:
    - trivy image --exit-code 1 --severity HIGH,CRITICAL ${IMAGE}
```

### Base Images

FlexInfer uses minimal base images:
- Controller, Proxy, Agent: `alpine:3.20` (~13MB)
- Backend images: Vendor base (ROCm, CUDA)

### Signing (Optional)

Enable cosign image signing:

```bash
cosign sign ${REGISTRY}/${IMAGE}:${TAG}
cosign verify ${REGISTRY}/${IMAGE}:${TAG}
```

## Audit Logging

Enable Kubernetes audit logging to track FlexInfer operations:

```yaml
# audit-policy.yaml
rules:
  - level: RequestResponse
    resources:
      - group: "ai.flexinfer"
        resources: ["*"]
  - level: Metadata
    resources:
      - group: ""
        resources: ["secrets"]
```

## Recommendations

### Production Checklist

1. **Enable network policies** with default-deny
2. **Use dedicated namespace** for FlexInfer
3. **Enable RBAC** (always on in modern Kubernetes)
4. **Use Pod Security Admission** (baseline or restricted)
5. **Scan images** before deployment
6. **Encrypt secrets** with SOPS or external secret operator
7. **Enable audit logging** for ai.flexinfer resources
8. **Limit GPU access** via node selectors/taints

### Hardening Steps

```bash
# 1. Enable network policies
helm upgrade flexinfer ./charts/flexinfer \
  --set networkPolicy.enabled=true \
  --set networkPolicy.defaultDeny=true

# 2. Apply Pod Security Admission
kubectl label namespace flexinfer-system \
  pod-security.kubernetes.io/enforce=baseline \
  pod-security.kubernetes.io/warn=restricted

# 3. Restrict node access
kubectl taint nodes gpu-node-1 gpu=true:NoSchedule
```

## Reporting Security Issues

Report security vulnerabilities to: security@flexinfer.ai

Please include:
- Description of the issue
- Steps to reproduce
- Potential impact
- Suggested fix (if any)
