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

## Debug a model that won’t become ready

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
   kubectl describe node <node> | rg -n \"nvidia.com/gpu|amd.com/gpu\"
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
