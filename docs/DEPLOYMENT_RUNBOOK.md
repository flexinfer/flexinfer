# FlexInfer Deployment Runbook

This document captures deployment experiences, issues encountered, and their solutions during real-world deployments of FlexInfer to mixed-GPU Kubernetes clusters.

## Environment

- **Cluster**: K3s v1.28
- **GPUs**: 2x AMD 7900 XTX (24GB VRAM), 1x NVIDIA GTX 980 Ti (6GB VRAM)
- **Registry**: Harbor (registry.harbor.lan)
- **GitOps**: Flux CD with HelmRelease

## Deployment Issues and Solutions

### 1. Agent Permission Denied on Binary

**Symptoms**:
```
exec /agent: permission denied
```

**Root Cause**: The agent Dockerfile used `gcr.io/distroless/static:nonroot` base image but didn't set the USER directive, causing permission issues when the container tried to execute the binary.

**Solution**: Add USER directive to `build/Dockerfile.agent`:
```dockerfile
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/agent .
USER 65532:65532
ENTRYPOINT ["/agent"]
```

**Lesson**: Always verify USER matches between builder and runtime stages in distroless images.

---

### 2. Agent DaemonSet Not Scheduling on GPU Nodes

**Symptoms**: Agent pods only running on non-GPU nodes, GPU nodes show 0 agents.

**Root Cause**: GPU nodes had a taint `dedicated=gpu:NoSchedule` for workload isolation, but the agent DaemonSet template didn't include tolerations.

**Solution**:
1. Add toleration support to Helm chart `templates/daemonset.yaml`:
```yaml
{{- with .Values.agent.tolerations }}
tolerations:
  {{- toYaml . | nindent 8 }}
{{- end }}
```

2. Configure tolerations in values.yaml:
```yaml
agent:
  tolerations:
    - key: "dedicated"
      operator: "Equal"
      value: "gpu"
      effect: "NoSchedule"
```

**Lesson**: DaemonSets for hardware discovery MUST tolerate any taints on the hardware nodes they need to access.

---

### 3. Agent Crash Loop on ARM64 Nodes

**Symptoms**:
```
exec format error
```

**Root Cause**: Agent binary built for amd64 only, but ARM64 nodes in the cluster attempted to run it.

**Solution**: Add nodeSelector to constrain agent to amd64:
```yaml
agent:
  nodeSelector:
    kubernetes.io/arch: amd64
```

**Future Work**: Build multi-arch images or add ARM64 support.

---

### 4. Agent Cannot Detect GPUs (No rocm-smi/nvidia-smi)

**Symptoms**: Agent runs but doesn't label nodes with GPU information. Logs show no GPU detection.

**Root Cause**: Distroless container image doesn't include `rocm-smi` or `nvidia-smi` utilities needed for GPU detection.

**Workaround**: Manually label GPU nodes:
```bash
kubectl label node <node-name> flexinfer.ai/gpu-present=true
kubectl label node <node-name> flexinfer.ai/gpu.vendor=AMD  # or NVIDIA
```

**Proper Fix**: Either:
1. Build agent image with GPU detection tools
2. Mount host GPU tools into container
3. Use sidecar with detection capabilities
4. Query Kubernetes node allocatable resources instead of system calls

---

### 5. Benchmark Job Sidecar Never Terminates

**Symptoms**: Benchmark job shows "Running 0/1 completions" even after benchmark completes. Pod shows 1/2 containers ready.

**Root Cause**: The benchmark uses a sidecar pattern (benchmarker + ollama). Kubernetes Jobs don't complete until ALL containers terminate. The ollama sidecar has no signal to shut down.

**Workaround**: Manually delete the job after benchmark results are written:
```bash
kubectl delete job <benchmark-job> -n flexinfer-system
```

**Proper Fix Options**:
1. Use `shareProcessNamespace: true` and have main container send SIGTERM to sidecar
2. Use shared volume with completion file that sidecar watches
3. Use Kubernetes 1.29+ native sidecar support
4. Add preStop hook to sidecar that checks main container status

---

### 6. Scheduler RBAC Insufficient Permissions

**Symptoms**: Model pods stuck in Pending with no events. Scheduler logs show permission denied errors:
```
services is forbidden: User "system:serviceaccount:flexinfer-system:flexinfer-scheduler" cannot list resource "services"
```

**Root Cause**: The kube-scheduler sidecar needs extensive cluster-wide permissions to function as a scheduler.

**Solution**: Bind scheduler service account to system:kube-scheduler role:
```bash
kubectl create clusterrolebinding flexinfer-scheduler-system \
  --clusterrole=system:kube-scheduler \
  --serviceaccount=flexinfer-system:flexinfer-scheduler
```

**Lesson**: Custom schedulers need the same permissions as kube-scheduler. Consider using the extender pattern instead to avoid duplicating scheduler permissions.

---

### 7. ModelDeployment Pods Missing GPU Tolerations

**Symptoms**: Benchmark and model pods fail to schedule on GPU nodes even when GPUs available:
```
0/15 nodes are available: 3 node(s) had untolerated taint {dedicated: gpu}
```

**Root Cause**: Controller creates pods without tolerations for GPU node taints.

**Solution**: Add tolerations to pod specs in `controllers/modeldeployment_controller.go`:
```go
Tolerations: []corev1.Toleration{
    {
        Key:      "dedicated",
        Operator: corev1.TolerationOpEqual,
        Value:    "gpu",
        Effect:   corev1.TaintEffectNoSchedule,
    },
},
```

**Future Improvement**: Make tolerations configurable via CRD spec or Helm values.

---

### 8. Backend Image Not in Private Registry

**Symptoms**: Pod fails to pull image:
```
Failed to pull image "registry.harbor.lan/library/ollama:rocm"
```

**Root Cause**: Custom backend image (ollama:rocm) not mirrored to private registry.

**Solution**: Use upstream image directly:
```yaml
backend:
  image:
    repository: ollama/ollama
    tag: "rocm"
```

**Alternative**: Mirror the image to private registry:
```bash
docker pull ollama/ollama:rocm
docker tag ollama/ollama:rocm registry.harbor.lan/library/ollama:rocm
docker push registry.harbor.lan/library/ollama:rocm
```

---

### 9. PVC Creation Fails - Storage Size 0

**Symptoms**: PVC created with 0 storage request, fails to provision.

**Root Cause**: ModelDeployment spec missing storage request in resources.

**Solution**: Add storage to spec:
```yaml
spec:
  resources:
    requests:
      storage: 20Gi
```

**Lesson**: Controller should validate and set default storage size if not specified.

---

### 10. Helm Chart Version Caching

**Symptoms**: Changes to Helm templates not being applied even after Flux reconciliation.

**Root Cause**: Flux caches Helm chart by version. Same version number means no re-render.

**Solution**: Bump chart version in `Chart.yaml`:
```yaml
version: 0.1.1  # was 0.1.0
```

**Lesson**: Always bump chart version when changing templates, or use Flux's `spec.chart.reconcileStrategy: Revision` for development.

---

## Verification Commands

### Check Component Health
```bash
# All FlexInfer pods
kubectl get pods -n flexinfer-system -o wide

# Agent labels on GPU nodes
kubectl get nodes -l flexinfer.ai/gpu-present=true --show-labels

# ModelDeployment status
kubectl get modeldeployments -n flexinfer-system

# Scheduler logs
kubectl logs deployment/flexinfer-scheduler -n flexinfer-system -c kube-scheduler
```

### Test Model Endpoint
```bash
# Check model availability
kubectl run curl-test --rm -i --restart=Never --image=curlimages/curl \
  -n flexinfer-system -- curl -s http://<model-svc>:11434/api/tags

# Test inference
kubectl run curl-test --rm -i --restart=Never --image=curlimages/curl \
  -n flexinfer-system -- curl -s http://<model-svc>:11434/api/generate \
  -d '{"model": "llama3:8b", "prompt": "Hello", "stream": false}'
```

### Check GPU Availability
```bash
# Node allocatable resources
kubectl get node <gpu-node> -o jsonpath='{.status.allocatable}' | jq .

# GPU usage
kubectl describe node <gpu-node> | grep -A5 "Allocated resources"
```

## Performance Results

### AMD 7900 XTX (24GB VRAM)
- Model: llama3:8b (4.6GB Q4_0)
- Benchmark TPS: 126.24 tokens/second
- Inference latency: ~7ms/token
- Model load time: 4.2s

### NVIDIA GTX 980 Ti (6GB VRAM)
- Model: phi3:mini (2.2GB Q4_0)
- Benchmark TPS: TBD
- Suitable for smaller models only

## Architecture Lessons Learned

1. **GPU Isolation**: Use taints/tolerations for GPU node isolation, but ensure all GPU-aware components (agents, model pods) tolerate those taints.

2. **Sidecar Pattern in Jobs**: Kubernetes Jobs with sidecars need explicit termination coordination. Consider native sidecar support (K8s 1.29+) or shared-volume signaling.

3. **Custom Schedulers**: Running a full kube-scheduler as sidecar requires extensive RBAC. Consider scheduler extender pattern for simpler permission model.

4. **Multi-Vendor GPU**: Supporting AMD (ROCm) and NVIDIA (CUDA) requires detecting GPU vendor and using appropriate container images. The controller should respect `spec.resources` GPU requests.

5. **GitOps with Helm**: Always bump chart versions when modifying templates. Use `imagePullPolicy: Always` during development.

## Future Improvements

- [ ] Fix agent GPU detection (add rocm-smi/nvidia-smi or query K8s resources)
- [ ] Implement benchmark job sidecar termination
- [ ] Add configurable tolerations to CRD spec
- [ ] Create proper scheduler ClusterRole (instead of binding to system:kube-scheduler)
- [ ] Add LiteLLM service discovery annotations
- [ ] Implement model pre-loading during deployment
