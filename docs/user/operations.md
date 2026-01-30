---
title: Operations
description: "Common day-2 workflows: inspect, debug, and clean up."
---

# Operations

## Inspect what’s running

```bash
kubectl -n flexinfer-system get deploy,ds,svc
kubectl -n flexinfer-system get pods -o wide
```

## Watch lifecycle state

### v1alpha2

```bash
kubectl -n flexinfer-system get models -w
kubectl -n flexinfer-system describe model <name>
```

### v1alpha1

```bash
kubectl -n flexinfer-system get modeldeployments -w
kubectl -n flexinfer-system describe modeldeployment <name>
```

## Debug a model that won't become ready

1. Check events:
   ```bash
   kubectl -n flexinfer-system describe pod <pod>
   ```
2. Check backend logs:
   ```bash
   kubectl -n flexinfer-system logs <pod> -c model --tail=200
   ```
3. Confirm GPU resources:
   ```bash
   kubectl get nodes -o wide
   kubectl describe node <node> | rg -n "nvidia.com/gpu|amd.com/gpu"
   ```

## NVIDIA GPU requirements

### Why `runtimeClassName: nvidia` is required

NVIDIA GPU workloads require the `nvidia` container runtime to function. FlexInfer automatically sets `runtimeClassName: nvidia` on pods requesting `nvidia.com/gpu` resources.

Without this runtime class:
- The pod may schedule successfully (it requests `nvidia.com/gpu` and a node has capacity)
- But `/dev/nvidia*` device nodes won't be mounted into the container
- CUDA will report no devices available

### Verifying NVIDIA runtime is working

1. Check if the runtime class exists:
   ```bash
   kubectl get runtimeclass nvidia
   ```

2. Verify devices are visible inside the pod:
   ```bash
   kubectl -n flexinfer-system exec <pod> -- ls /dev/nvidia*
   # Should show: /dev/nvidia0 /dev/nvidiactl /dev/nvidia-uvm ...
   ```

3. Check CUDA availability:
   ```bash
   kubectl -n flexinfer-system exec <pod> -- python -c "import torch; print(torch.cuda.is_available())"
   # Should print: True
   ```

### Common failure symptoms

| Symptom | Likely Cause |
|---------|--------------|
| `torch.cuda.is_available() == False` | Missing `runtimeClassName: nvidia` or NVIDIA driver not installed |
| Pod stuck in `ContainerCreating` | RuntimeClass `nvidia` doesn't exist on the cluster |
| `CUDA error: no CUDA-capable device is detected` | Device nodes not mounted; check runtime class |
| Pod runs but inference is slow | Fell back to CPU; check device availability |

### Cluster prerequisites

1. NVIDIA device plugin must be deployed (creates `nvidia.com/gpu` resources)
2. NVIDIA container runtime must be installed on GPU nodes
3. RuntimeClass `nvidia` must exist:
   ```yaml
   apiVersion: node.k8s.io/v1
   kind: RuntimeClass
   metadata:
     name: nvidia
   handler: nvidia
   ```

## Clean up (important: delete parents)

FlexInfer resources are hierarchical. Delete the parent, not the children.

- v1alpha2: delete the `Model`
  ```bash
  kubectl -n flexinfer-system delete model <name>
  ```
- v1alpha1: delete the `ModelDeployment` (not the Deployment)
  ```bash
  kubectl -n flexinfer-system delete modeldeployment <name>
  ```

For detailed cleanup guidance (including RAM caches and stuck Jobs), see the “Resource Cleanup Procedures” section in `services/flexinfer/AGENTS.md`.
