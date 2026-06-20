# Runbook: GPU lease kill-test (Slice 1 riskiest assumption)

- **Date**: 2026-06-20
- **Spec**: [spec-training-shared-gpu-lease-2026-06-20.md](spec-training-shared-gpu-lease-2026-06-20.md)
- **Status**: not run (procedure ready; needs controller image with `feat/gpu-lease-scheduler` rolled out)

## What this proves

The load-bearing assumption: a scheduler-honored GPU lease can **park-and-hold** the
serving incumbent on a shared card, free `amd.com/gpu` for a training Job, and on
release **re-promote** serving cleanly — with no strand, no flap, no double-booked card.

This runbook drives the lease **manually** (a ConfigMap + a probe Job). The
finetune-controller acquire/release loop (slice 3) is NOT required to exercise the
assumption.

## Prerequisites

1. Controller image built from `feat/gpu-lease-scheduler` (merged) and rolled out:
   `kubectl -n flexinfer rollout status deploy/flexinfer-controller`
2. `kubectl` against the k3s app cluster (`KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml`).
3. Confirm the target group + incumbent (the docs reference the 5930k text-gen group;
   verify the live names — do not assume):
   ```bash
   kubectl -n flexinfer get models.ai.flexinfer -o custom-columns=\
   NAME:.metadata.name,GROUP:.spec.gpu.shared,PRIO:.spec.gpu.priority,PHASE:.status.phase,STATE:.status.sharedGroup.state
   ```
   Pick `GROUP` (call it `$GROUP`) and note the currently-`Ready`/`Active` incumbent (`$INCUMBENT`)
   and its node (`$NODE`).

## Procedure

### 1. Baseline

```bash
kubectl -n flexinfer get model $INCUMBENT \
  -o jsonpath='{.status.phase} {.status.sharedGroup.state}{"\n"}'   # expect: Ready Active
kubectl -n flexinfer get pods -l ai.flexinfer/model=$INCUMBENT      # expect: 1 Running
```

### 2. Acquire the lease (park-and-hold)

Apply the lease ConfigMap (10-minute TTL backstop). The election picks it up on the
next reconcile (≤ `requeueFast` = 3s):

```bash
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EXP=$(date -u -v+10M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+10 min' +%Y-%m-%dT%H:%M:%SZ)
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: gpu-lease-$GROUP
  namespace: flexinfer
  labels:
    ai.flexinfer/gpu-lease: "$GROUP"
data:
  group: "$GROUP"
  node: "$NODE"
  owner: "killtest-manual"
  acquiredAt: "$NOW"
  expiresAt: "$EXP"
EOF
```

**Assert (within ~1 `SwapCooldown`, watch for ~30s):**
```bash
kubectl -n flexinfer get model $INCUMBENT \
  -o jsonpath='{.status.phase} {.status.sharedGroup.state} {.status.sharedGroup.preemptedBy}{"\n"}'
# expect: Preempted Queued gpu-lease/killtest-manual
kubectl -n flexinfer get pods -l ai.flexinfer/model=$INCUMBENT
# expect: scaling to 0 → no Running pod (the card frees)
```

### 3. Run the probe Job on the freed card

```bash
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: gpu-lease-probe
  namespace: flexinfer
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 300
  template:
    spec:
      restartPolicy: Never
      nodeSelector: { kubernetes.io/hostname: "$NODE" }
      tolerations:
        - { key: dedicated, operator: Equal, value: gpu, effect: NoSchedule }
      containers:
        - name: probe
          image: registry.harbor.lan/flexinfer/runtime:unified-gfx906-v3   # any ROCm img w/ python+torch
          command: ["/bin/bash","-c"]
          args: ["python -c 'import torch; print(\"cuda_available\", torch.cuda.is_available()); assert torch.cuda.is_available()' || rocm-smi"]
          resources:
            limits: { amd.com/gpu: "1" }
EOF
```

**Assert (the real test — the Job must BIND, not sit Pending on `Insufficient amd.com/gpu`):**
```bash
kubectl -n flexinfer wait --for=condition=complete job/gpu-lease-probe --timeout=180s
kubectl -n flexinfer logs job/gpu-lease-probe   # expect: cuda_available True
```

### 4. Release the lease (re-promote serving)

```bash
kubectl -n flexinfer delete configmap gpu-lease-$GROUP
```

**Assert (within ~1 `SwapCooldown`):**
```bash
kubectl -n flexinfer get model $INCUMBENT \
  -o jsonpath='{.status.phase} {.status.sharedGroup.state}{"\n"}'   # expect: ...→ Ready Active
# serving round-trip:
kubectl -n flexinfer exec deploy/flexinfer-proxy -- \
  curl -sS localhost:8000/model/$INCUMBENT/v1/models | head -c 200
```

## Pass criteria

- Probe Job reaches `Completed` on the freed card (proves the slot was actually freed).
- `$INCUMBENT` returns to `Ready`/`Active` and serves a request within one `SwapCooldown` of release.
- No double-book: the probe Job and a serving pod were never `Running` on the card simultaneously
  (verify pod timelines: incumbent pod `Terminated` before probe pod `Running`).

## Negative / crash-safety checks

- **Stale lease (dead acquirer)**: apply a lease with `expiresAt` in the past → assert
  the incumbent does NOT park (election ignores expired leases). TTL backstop.
- **Mid-run release**: delete the lease while the probe Job is still `Running` → assert
  no double-book (serving should wait for the card to free; the probe holds `amd.com/gpu`
  until it exits, so serving re-promote may briefly `Pending` on `Insufficient amd.com/gpu`
  — that's expected back-pressure, not a strand).
- **Lease read error fail-open**: covered in code (handleSharedGPU proceeds as unleased on
  read error) — not separately testable live without injecting an API fault.

## Rollback

```bash
kubectl -n flexinfer delete configmap gpu-lease-$GROUP --ignore-not-found
kubectl -n flexinfer delete job gpu-lease-probe --ignore-not-found
# incumbent re-promotes automatically once the lease is gone.
```
