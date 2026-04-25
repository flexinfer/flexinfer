# Gemma4 Abliteration + GPTQ Pipeline: ROCm Retrospective

**Date**: 2026-04-11
**Models**: Gemma4-31B-it (dense, gfx906), Gemma4-26B-A4B-it (MoE, gfx1100)
**Pipeline**: Download BF16 -> Abliterate -> GPTQ INT4 -> Ready
**Duration**: 2 sessions, ~6 hours wall-clock
**Commits**: `82bdad84`, `033901d2`, `cd5f0da9`

---

## Timeline

### Session 1: Initial Pipeline Fixes (09:00 - 10:30)

| Time  | Event | Outcome |
|-------|-------|---------|
| 09:00 | Deployed Gemma4 ModelCache CRDs, triggered pipeline | Download started |
| 09:15 | Abliteration job failed: `KeyError: 'gemma4_text'` in transformers | transformers on PyPI lacks Gemma4 model_type |
| 09:25 | **Fix 1**: Pinned transformers to git commit `f965b10b` in GPTQ policy, wired `ABLITERATION_TRANSFORMERS_PACKAGE` | Abliteration recognized the model |
| 09:40 | 26B GPTQ crashed: `RuntimeError` on 3D expert tensor shape | MoE fused expert weights incompatible with GPTQModel 2D quantization |
| 09:55 | **Fix 2**: Added MoE auto-detection in `quantize_gptq.py` (`num_local_experts` config key), restricted abliteration to `o_proj` only for MoE | Expert tensors excluded from quantization |
| 10:05 | 31B abliteration stalled on radeonvii: disk offload I/O dominated (CPU budget 56GB for 61GB model) | Legacy 56GB cap forced excessive disk offload |
| 10:15 | **Fix 3**: Removed hardcoded cap from `abliterationCPUMaxMemoryGB()`, formula now scales as `containerMem - 20` capped at `containerMem * 0.8` | 96GB container -> 76GB CPU budget |
| 10:25 | **Fix 4**: Corrected YAML comments (architecture details wrong in both CRDs) | — |
| 10:30 | Pushed commit `82bdad84`, rebuilt controller, redeployed | Both pipelines restarted |

### Session 2: Deploy, Debug, Stabilize (10:45 - 13:00)

| Time  | Event | Outcome |
|-------|-------|---------|
| 10:45 | Controller rebuild pushed to Harbor. `kubectl rollout restart` pulled old digest. | **Op Issue 1**: Flux digest pinning |
| 10:55 | Used `kubectl set image` with explicit new digest, waited for rollout | New controller running |
| 11:00 | Deleted 31B abliteration job to pick up new env vars. Old controller (still running during rollout) recreated job with stale config. | **Op Issue 2**: Rolling update race |
| 11:05 | Waited for rollout completion, deleted and let new controller recreate 31B job | Job running with correct env vars |
| 11:15 | 31B abliteration on gfx906 crashed during save: OOM at 60GB RSS | `mergeEnvVars()` silently overrode 76GB CPU budget back to 56GB |
| 11:25 | **Fix 5**: Changed CPU memory logic from last-write-wins to `max(formula, env, GPUProfile)` | CPU budget correctly resolved to 76GB |
| 11:30 | 31B abliteration crashed again during save: `hipErrorInvalidValue` on GPU->CPU tensor copy | gfx906 VMM limitation prevents materialized state_dict export |
| 11:40 | **Fix 6**: Defaulted `ABLITERATION_DISK_OFFLOAD_SAVE_IMPL=streaming` for gfx906 | Streaming save pulls one module at a time, avoids bulk GPU->CPU copy |
| 11:45 | Pushed commit `033901d2`, rebuilt controller (2nd rebuild) | 31B restarted on radeonvii |
| 12:00 | 26B abliteration completed all 63 shards, then OOM-killed during post-save cleanup | Container limit 48Gi, RSS peaked at 50.8GB |
| 12:05 | Observed: script had deleted 3 original weight files before save completed. Pod died. Controller detected missing source and reset to full download. | **Op Issue 3**: Destructive cleanup before finalization |
| 12:10 | **Fix 7**: Bumped `maxMemoryGB` from 48 to 56 in 26B CRD (container limit -> 60Gi with driver offset) | Adequate headroom for streaming save |
| 12:15 | Pushed commit `cd5f0da9` | 26B re-downloading (~8 min penalty from Op Issue 3) |
| ~13:00 | Both 26B and 31B pipelines progressing through abliteration with correct configuration | Stabilized |

---

## Fix Details

### Fix 1: Transformers Git Pin for Gemma4 Support

**Problem**: Gemma4 `model_type: "gemma4_text"` not recognized by the PyPI transformers release.

**Root cause**: The abliteration script installed custom transformers from `ABLITERATION_TRANSFORMERS_PACKAGE`, but the GPTQ quantization script had no equivalent mechanism. Both scripts needed the same unreleased Gemma4 support.

**Fix**: Added `git+https://github.com/huggingface/transformers.git@f965b10b` to the Gemma4 model policy in `gptq.go` (line 234) and wired `ABLITERATION_TRANSFORMERS_PACKAGE` from the GPUProfile.

**Files**: `pkg/quantization/gptq.go`, `pkg/quantization/abliteration.go`

### Fix 2: MoE Expert Exclusion for 26B

**Problem**: GPTQModel's quantization kernel operates on 2D weight matrices. Gemma4-26B-A4B's MoE architecture has fused 3D expert tensors (`[num_experts, hidden, intermediate]`) that crash the quantizer.

**Root cause**: The `dynamicExclusion` feature in `quantize_gptq.py` existed but only triggered when explicitly set. Auto-detection of MoE via `num_local_experts` was not implemented.

**Fix**: Added `auto` mode to `quantize_gptq.py` that reads `num_local_experts` from `config.json` and excludes `.*experts.*` and `.*block_sparse_moe.*` patterns. Also restricted abliteration to `o_proj` only for MoE (expert FFN weights must not be modified).

**Files**: `build/scripts/quantize_gptq.py`, `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml`

### Fix 3: CPU Memory Cap Scaling

**Problem**: 31B BF16 model (~61GB) on radeonvii (128GB RAM, 96GB container) was thrashing on disk offload. Abliteration took hours instead of minutes per layer.

**Root cause**: `abliterationCPUMaxMemoryGB()` used `min(containerMem * 0.8, 56)`. The 56GB cap was a legacy from when all models ran on 64GB nodes. A 96GB container got the same 56GB budget as a 64GB container, forcing 5GB of model weights to disk.

**Fix**: Removed the hardcoded 56GB cap. Formula is now `min(containerMem - 20, containerMem * 0.8)` with a 12GB floor.

**File**: `pkg/quantization/abliteration.go:106-122`

### Fix 4: CRD YAML Comment Corrections

**Problem**: Both ModelCache CRDs had incorrect architecture descriptions (e.g., "all standard attention" instead of "GDN hybrid").

**Fix**: Updated comments to accurately describe the GDN hybrid architecture, MoE structure, and layer counts.

### Fix 5: CPU Memory Precedence (mergeEnvVars Silent Override)

**Problem**: After Fix 3 gave the formula a 76GB budget for the 96GB container, the 31B abliteration still crashed with OOM during save. RSS grew to 60GB and `model.state_dict()` doubled it.

**Root cause**: `mergeEnvVars()` applies last-write-wins semantics. The env var construction in `BuildAbliterationJob()` is:

```go
// abliteration.go:200-201
env = mergeEnvVars(env, ablitEnv)      // formula: CPU=76
env = mergeEnvVars(env, params.ProfileEnv) // GPUProfile: CPU=56 (overwrite!)
```

The GPUProfile's `maxCPUMemoryGB: 56` silently overwrote the formula's 76GB. No logging indicated the override occurred.

**Fix**: Changed CPU memory resolution from `mergeEnvVars()` last-write-wins to explicit priority:

```go
// abliteration.go:338-353
formulaCPU := abliterationCPUMaxMemoryGB(maxMemoryGB)
cpuMaxMemoryGB := fmt.Sprintf("%d", formulaCPU)
if envCPU := os.Getenv("FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB"); envCPU != "" {
    if v, err := strconv.ParseInt(envCPU, 10, 32); err == nil && int32(v) > formulaCPU {
        cpuMaxMemoryGB = envCPU  // env only wins if LARGER
    }
}
```

The key change: env var and GPUProfile overrides only apply when they provide a **larger** value than the formula, not when they're smaller.

**File**: `pkg/quantization/abliteration.go:338-353`

### Fix 6: gfx906 Streaming Save Default

**Problem**: Even with correct CPU memory (76GB), the 31B save on gfx906 hit `hipErrorInvalidValue`.

**Root cause**: The default save implementation calls `model.state_dict()`, which iterates all parameters and moves them to CPU. On gfx906 (Vega20), the VMM subsystem is not supported -- HIP cannot perform the GPU-to-CPU tensor copy for state_dict export. This is the same hardware limitation that requires `patch-hipmemgetinfo.sh` at runtime.

**Fix**: Default `ABLITERATION_DISK_OFFLOAD_SAVE_IMPL=streaming` when `gpuArch == "gfx906"`. Streaming save pulls one module at a time via `align_module_device()`, which handles the GPU/CPU/disk boundaries individually rather than bulk-materializing.

```go
// abliteration.go:327-333
diskOffloadSaveImpl := os.Getenv("FLEXINFER_ABLITERATION_DISK_OFFLOAD_SAVE_IMPL")
if diskOffloadSaveImpl == "" && gpuArch == "gfx906" {
    diskOffloadSaveImpl = "streaming"
}
```

**File**: `pkg/quantization/abliteration.go:327-333`

### Fix 7: 26B Save-Phase OOM

**Problem**: 26B abliteration completed all 63 shards successfully, then the pod was OOM-killed during post-save cleanup. RSS peaked at 50.8GB against a 48Gi container limit.

**Root cause**: Streaming save migrates GPU tensors to CPU one module at a time, but the transient CPU copy adds ~1.4GB of overhead. With 48Gi limit, this left only ~700MB headroom -- not enough for Python's garbage collector and the OS.

**Fix**: Bumped `maxMemoryGB` from 48 to 56 in the 26B CRD. Container limit becomes ~60Gi after GPU driver memory offset, providing adequate headroom.

**File**: `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml:55`

---

## Operational Issues

### Op Issue 1: Controller Image Digest Pinning

**Symptom**: After rebuilding the controller and pushing to Harbor, `kubectl rollout restart` pulled the old image.

**Root cause**: Flux pins container images by digest (`@sha256:...`), not by tag (`:master`). A `rollout restart` re-pulls the same pinned digest, ignoring the new image behind the `:master` tag.

**Workaround**: Used `kubectl set image` with the explicit new digest extracted from `docker inspect`.

**Impact**: ~10 minutes debugging why the new code wasn't running.

### Op Issue 2: Old Controller Race During Rolling Update

**Symptom**: Deleted the 31B job so the new controller would recreate it with correct env vars. The old controller (still running during the rolling update) picked up the deletion event and recreated the job with stale configuration.

**Root cause**: Kubernetes rolling updates keep the old pod running until the new pod passes its readiness probe. Both pods are watching the same resources. The old pod handles the deletion event first because it is already fully warmed up.

**Workaround**: Wait for `kubectl rollout status` to report success, then delete jobs.

**Impact**: ~10 minutes of wasted job execution with wrong env vars.

### Op Issue 3: Source Weight Cleanup Before Finalization

**Symptom**: The 26B abliteration script logged "Removed 3 old weight artifacts from source dir" during the save phase. When the pod OOM-killed 2 minutes later, both the original BF16 weights and the in-progress abliterated weights were lost.

**Root cause**: The cleanup logic deletes old weight files during save (to free disk space) before the atomic rename of staged output to the final location. If the save fails mid-way, both copies are gone.

**Impact**: Forced a full re-download of the 26B model (~27GB, ~8 minutes), adding an unnecessary pipeline restart.

---

## Summary Statistics

| Metric | Value |
|--------|-------|
| Code fixes | 7 (4 in session 1, 3 in session 2) |
| Commits | 3 (`82bdad84`, `033901d2`, `cd5f0da9`) |
| Files modified | 6 (abliteration.go, abliteration_test.go, gptq.go, quantize_gptq.py, 2x model CRDs) |
| Controller rebuilds | 2 |
| Full pipeline restarts | 3+ (31B retried 3 times, 26B restarted from download once) |
| Net lines changed | ~165 added, ~55 removed |
| Wasted compute time | ~2 hours (failed jobs, re-downloads, stale controller) |

---

## Root Cause Analysis

### 1. `mergeEnvVars()` Last-Write-Wins Is a Silent Override Trap

**Severity**: High -- caused Fix 5 and wasted an entire pipeline restart cycle.

The `mergeEnvVars()` function in `gpu_job.go:73-91` replaces existing env vars by name with no logging:

```go
for _, item := range additional {
    if idx, ok := indexByName[item.Name]; ok {
        out[idx] = item  // silent overwrite
        continue
    }
}
```

When `BuildAbliterationJob()` calls `mergeEnvVars(env, params.ProfileEnv)` as the last step, GPUProfile values silently overwrite controller-computed values. A developer inspecting `abliterationEnv()` sees the formula produce 76GB, but the final pod gets 56GB. The only way to discover this is to `kubectl describe` the running pod and compare env vars.

This is particularly dangerous because:
- The formula and the ProfileEnv override compute the same variable (`ABLITERATION_CPU_MAX_MEMORY_GB`)
- The formula is model-size-aware; the ProfileEnv is a static default
- Nothing in the logs or events indicates which value won

### 2. No Save-Phase Memory Accounting in Container Limits

**Severity**: Medium -- caused Fix 7 and an OOM after successfully completing the expensive work.

Container `maxMemoryGB` is set to fit the model during abliteration (forward pass + activation captures). But the save phase has its own memory profile: streaming save migrates tensors from GPU to CPU, temporarily holding both the model weights and a per-module CPU copy. For a 26B MoE model with 63 shards, this adds ~1-2GB of transient overhead that isn't in the budget.

The failure mode is especially painful: the job does 2+ hours of GPU compute successfully, then OOMs during the final 5-minute save. All work is lost.

### 3. gfx906 Quirks Not Uniformly Defaulted

**Severity**: Medium -- caused Fix 6 and duplicates known patterns.

The Radeon VII's VMM limitation affects multiple subsystems:
- `hipMemGetInfo` (patched at runtime via `patch-hipmemgetinfo.sh`)
- Caching allocator warmup (skipped via `ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP`)
- Safe sharded load (enabled via `ABLITERATION_SAFE_SHARDED_LOAD`)
- State dict export during save (**not** defaulted until Fix 6)

Each quirk was discovered and patched independently. There is no unified "gfx906 compatibility mode" that enables all workarounds together. When a new code path touches GPU memory (like save), it re-discovers the VMM limitation.

### 4. No Pre-flight Memory Validation

**Severity**: Medium -- would have caught Fixes 3, 5, and 7 before any job started.

The pipeline does not validate that the container memory limit is sufficient for the model before committing to a multi-hour job. A 61GB BF16 model in a 56GB CPU budget will always fail during save, but the pipeline runs for 2+ hours before discovering this. A simple check -- `model_size_bytes * 1.1 > container_limit` -- would fail fast at job creation time.

### 5. Controller Deploy Workflow Is Fragile

**Severity**: Low (operational friction, not correctness) -- caused Op Issues 1 and 2.

The deploy cycle requires:
1. `docker --context 7900xtx build` (remote)
2. `docker push` to Harbor
3. Extract new digest from `docker inspect`
4. `kubectl set image` with new digest (because Flux pins digests)
5. `kubectl rollout status --watch` (wait for completion)
6. Delete old jobs (after step 5, not before)
7. Verify new jobs have correct env vars

Missing any step or reordering steps 5 and 6 produces hard-to-debug failures. The current manual process has 3 opportunities for human error.

### 6. Source Weight Cleanup Before Finalization Is Destructive

**Severity**: Low (8-minute penalty, not data loss) -- caused Op Issue 3.

The abliteration script cleans old weight files during save to reclaim disk space. If the save fails after cleanup, both the original and in-progress weights are gone. The controller detects missing source weights and resets to a full download.

The correct sequence is: save to staging area -> atomic rename -> clean old files. The current sequence is: clean old files -> save in place.

---

## Recommendations

### Short-Term (Next Sprint)

**1. Log env var resolution in `mergeEnvVars()`**

Add a debug log when an existing env var is overwritten:

```go
if idx, ok := indexByName[item.Name]; ok {
    if out[idx].Value != item.Value {
        log.V(1).Info("env var override", "name", item.Name,
            "old", out[idx].Value, "new", item.Value)
    }
    out[idx] = item
}
```

This would have made Fix 5 a 2-minute diagnosis instead of 30 minutes.

**Effort**: Small. **Impact**: High (debugging time savings on every future env var issue).

**2. Add save headroom to container limits**

When computing `schedulingMemoryGB`, add a save-phase buffer:

```go
if ablitSpec.UseGPU {
    schedulingMemoryGB += abliterationGPUMaxMemoryGB(useGPU, gpuArch) / 2
}
```

For the 26B case: 48 + 10 = 58Gi, which would have survived without Fix 7.

**Effort**: Small. **Impact**: Medium (prevents save-phase OOM for all future models).

**3. Pre-flight memory validation**

Before creating an abliteration job, check:

```go
modelSizeGB := estimateModelSizeGB(modelConfig)  // from config.json param count + dtype
cpuBudget := abliterationCPUMaxMemoryGB(maxMemoryGB)
if modelSizeGB > float64(cpuBudget) * 0.95 {
    log.Error("model size exceeds CPU budget, save will likely OOM",
        "modelSizeGB", modelSizeGB, "cpuBudgetGB", cpuBudget)
    // emit event on ModelCache CR
}
```

**Effort**: Medium (needs `config.json` parsing at job creation time). **Impact**: High (prevents wasted multi-hour jobs).

**4. Defer source cleanup to after finalization**

Move weight cleanup in `abliterate.py` from during-save to after-atomic-rename:

```python
# Current (dangerous):
cleanup_old_weights()
save_streaming()

# Fixed:
save_streaming(staging_dir)
atomic_rename(staging_dir, final_dir)
cleanup_old_weights()
```

**Effort**: Small. **Impact**: Low (prevents re-download on save failure, ~8 min per occurrence).

### Medium-Term (Next Month)

**5. Integration tests with real model sizes**

Add a test in `abliteration_test.go` that validates env var computation for known ModelCache CRDs:

```go
func TestGemma4EnvVarResolution(t *testing.T) {
    params := JobParams{
        MemoryConfig: MemoryConfig{ContainerMemoryGB: 96, MaxCPUMemoryGB: 56},
        ProfileEnv:   []corev1.EnvVar{{Name: "ABLITERATION_CPU_MAX_MEMORY_GB", Value: "56"}},
    }
    env := abliterationEnv(...)
    cpuVal := findEnvVar(env, "ABLITERATION_CPU_MAX_MEMORY_GB")
    assert.GreaterOrEqual(t, cpuVal, "76")  // formula should win over GPUProfile
}
```

**Effort**: Medium. **Impact**: High (catches mergeEnvVars regressions at test time, not deploy time).

**6. Controller deploy automation**

Create `build/scripts/deploy-controller.sh`:

```bash
#!/bin/bash
set -euo pipefail
IMAGE=registry.harbor.lan/flexinfer/flexinfer-controller:master
docker --context 7900xtx build -f build/Dockerfile.manager -t "$IMAGE" .
docker --context 7900xtx push "$IMAGE"
DIGEST=$(docker --context 7900xtx inspect --format='{{index .RepoDigests 0}}' "$IMAGE")
kubectl set image deployment/flexinfer-controller -n flexinfer-system manager="$DIGEST"
kubectl rollout status deployment/flexinfer-controller -n flexinfer-system --timeout=120s
echo "Controller deployed: $DIGEST"
```

**Effort**: Small. **Impact**: Medium (eliminates 3 manual error opportunities per deploy).

**7. Replace `mergeEnvVars()` with explicit priority chain**

Instead of list-append-then-overwrite, use a structured resolution:

```go
type EnvSource struct {
    Name     string
    Value    string
    Priority int  // higher wins
    Source   string // "formula", "env", "gpuprofile" -- for logging
}
```

Each env var is resolved by selecting the highest-priority source with a non-empty value. All resolutions are logged.

**Effort**: Medium (refactor mergeEnvVars callers). **Impact**: High (eliminates the entire class of silent-override bugs).

### Long-Term (Next Quarter)

**8. Adaptive container memory from model metadata**

Read `config.json` at job creation time, compute:

```
base_memory = num_params * bytes_per_param(dtype)
save_overhead = base_memory * 0.05  // streaming save transient
gpu_driver = 4Gi  // or read from GPUProfile
container_limit = base_memory + save_overhead + gpu_driver + 8Gi  // OS/python headroom
```

This replaces manual `maxMemoryGB` tuning in every CRD with auto-sizing that adapts to any model.

**Effort**: Large (needs config.json parsing, dtype mapping, model-type-aware estimation). **Impact**: High (eliminates the #1 source of pipeline churn: memory misconfiguration).

**9. gfx906 compatibility manifest**

Create a single `gfx906Defaults()` function that returns all required workarounds:

```go
func gfx906Defaults() map[string]string {
    return map[string]string{
        "ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP": "true",
        "ABLITERATION_SAFE_SHARDED_LOAD":             "true",
        "ABLITERATION_DISK_OFFLOAD_SAVE_IMPL":        "streaming",
        "HSA_OVERRIDE_GFX_VERSION":                    "9.0.6",
    }
}
```

New code paths that touch GPU memory check this manifest instead of re-discovering VMM limitations.

**Effort**: Small. **Impact**: Medium (prevents future gfx906 regressions when adding new features).

---

## Lessons Learned

1. **Silent overrides are worse than crashes.** A crash at least tells you something is wrong. `mergeEnvVars()` silently producing the wrong value cost more debugging time than all the crashes combined.

2. **Save-phase failures are the most expensive.** An OOM during save wastes the entire compute budget of the job. Memory budgets must account for peak save-phase usage, not just steady-state inference.

3. **gfx906 is a reliability tax.** Every new feature path (abliteration, save, quantization) re-discovers the Vega20 VMM limitation. The cost of maintaining gfx906 compatibility is real and should be tracked.

4. **Manual deploy workflows don't scale.** Two controller rebuilds with 5+ manual steps each. Automation would have saved 20+ minutes and prevented Op Issues 1 and 2.

5. **MoE models need explicit pipeline support.** The MoE architecture (fused 3D expert tensors, restricted weight matrices for abliteration) is different enough from dense models that it needs first-class support in the pipeline, not after-the-fact exclusion patterns.
