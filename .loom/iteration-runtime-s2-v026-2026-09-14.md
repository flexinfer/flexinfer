# Runtime S2: vLLM 0.26 canary continuation

## Scope

Continue Fable's draft !1058 for issue #74. Test the rebuilt native-W4A16
runtime on a dedicated 5930k debug Model before changing production pins.
The canary is 64K; production 128K context and other lanes' LoRA envelopes
need independent qualification even if this smoke passes.

## Artifact pinning

- Branch: `feat/runtime-s2-v026-debug-canary`.
- Image: `registry.harbor.lan/flexinfer/runtime@sha256:ad93ae726bbeb27b2731e3d9ca1ba8f0a9d4234ad7c6d58ef6423e4dc40beeb5`.
- CI tag: `rocm-gfx1100-v026-native-w4a16-a106504c`, pipeline 26906.
- Model: `qwen38-27b-v026-canary`, node `cblevins-5930k`.
- Source: `biMEMO/Qwen3.8-27B-int4-AutoRound`, observed revision
  `2d7425f78ee0abd518aceabf385997fbb54c9342` (now pinned in the probe manifest).
  All 18 non-README file ETags match the production cache's
  `17c77026ea52b9d1c5e02efcb822974e25af57fe` revision, including all eight
  safetensors files. Only the README ETag differs.
- Local model directory:
  `/var/lib/flexinfer/models/flexinfer-system/qwen38-27b-v026-canary`.
- Isolated compile cache:
  `/var/lib/flexinfer/compile-cache-qwen38-27b-v026-5930k`.

## Preflight and baseline

Both text lanes were Ready; the 5930k had no running/waiting inference requests
and no DiskPressure or MemoryPressure. Mills' weaver fallback was the Ready
`qwen38-27b-xtx-warm-canary`. The target filesystem had 235 GiB free after staging.

`scripts/verify-vllm-canary.py` on the v0.23 workhorse passed 3/3 coherence
probes and default non-thinking behavior. The recall prompt used 2,337 tokens:
TTFT **2.308 s**. Three 256-token novel-generation samples reached
**41.6722 / 41.6907 / 41.5586 tok/s**, median **41.6722**. These are direct
in-pod streaming measurements; the 128K production configuration is a reference,
not a fully matched 64K control.

Production `/models/config.json` SHA256:
`85282ee6ad317ec6e254a4d3577c4ea807d4992cb024fb29205f056c3ce7ec9b`.
Preflight mtimes: 5930k `1786896518559879782` ns; XTX
`1789321153339533406` ns. The new cache's config, tokenizer config, and index
hashes match production before startup.

## Iteration 1: make the hidden canary start

The original draft combined `warmPolicy: ondemand` and `litellm.enabled: false`.
After staging it remained Idle/Queued behind the Ready workhorse, and the
proxy returned 404 without recording demand. This is the intended parked-model
behavior in `internal/proxy/proxy.go` and `serving_policy.go`.

The canary now uses `warmPolicy: primary`, keeping discovery disabled. It
reclaimed the idle shared slot. `minReplicas: 1` is not a cleanup TTL: delete
the parent Model when the experiment ends. A direct in-pod HTTP script replaces
the missing `s2-killtest.py` reference; it has a ten-minute wall-clock bound
and ten tests for incomplete streams, missing usage, default reasoning, and
latency/throughput gates.

## Iteration 2: original 0.94 memory budget — FAIL

- Model created 23:36:55Z; cache staging finished 23:41:56Z.
- Warm-primary patch elected the canary; pod
  `qwen38-27b-v026-canary-84b5796cb5-4srqk` created 23:45:11Z.
- Container started 23:49:33Z with the exact `ad93ae72…` imageID.
- Plugin registration passed with `fla_autotuners=11`.
- vLLM 0.26 selected `RDNA3W4A16LinearKernel` and `TRITON_ATTN`.
- Weights loaded in 25.49 s, using 16.77 GiB. Fresh graph compilation completed.
- Engine exited 1 at 23:53:30Z; one restart. No HTTP serving or probe result.

Exact blocker, `vllm/v1/core/kv_cache_utils.py:778`:

```text
To serve at least one request with the model's max seq len (65536),
(2.15 GiB KV cache is needed, which is larger than the available KV cache
memory (1.92 GiB). ... estimated maximum model length is 58016.
```

The original 0.94 tuple fails the advertised 64K gate. The plugin/import
blocker is retired; KV capacity is the next measured constraint.

## Iteration 3: 0.96 memory budget

Hypothesis: allocating another 2% of the 24 GiB GPU covers the 0.23 GiB KV
shortfall. Change only `gpuMemoryUtilization` to `"0.96"`; keep the image,
model files, 64K limit, FP8 KV, and isolated compile cache unchanged.

The retry pod `qwen38-27b-v026-canary-54f9699c87-sht9v` became Ready at
23:57:54Z with zero restarts. It reused compiled graphs (5.228 s load),
reported **4.26 GiB KV / 129,615 tokens**, and passed 3/3 probes. The 2,337-token
recall TTFT was **2.3015 s**; novel decode samples **41.6844 / 41.5100 / 41.5778
tok/s**, median **41.5778** (−0.23% versus the warm production reference).
The first short request paid 7.831 s of additional warmup; subsequent short
TTFT was 0.16–0.20 s. This is runtime currency with comparable warm speed,
not evidence of a throughput improvement.

The KV gain (2.34 GiB) is much larger than 2% of 24 GiB. Compile-cache warmth
changed too, so the pass alone cannot attribute recovery to utilization.

## Iteration 4: 0.96 with a fresh compile cache

Change only the compile-cache hostPath to
`/var/lib/flexinfer/compile-cache-qwen38-27b-v026-cold096-5930k`, retaining
0.96 utilization and the 64K envelope. This checks first-boot robustness
instead of relying on graphs produced by the failed 0.94 attempt.

Pod `qwen38-27b-v026-canary-6d9d976fb7-54rld` used the exact `ad93ae72…`
image and reached Ready with zero restarts. Fresh `torch.compile` took
**88.55 s**. At 00:03:46Z on September 15, the engine reported **2.40 GiB KV /
72,817 tokens**; graph capture took 2 s / 0.04 GiB. This passes the 64K startup
capacity gate without warm compile artifacts.

HTTP probes passed 3/3. The 2,337-token recall TTFT was **2.3112 s**; novel
decode samples **41.3745 / 40.7498 / 41.2730 tok/s**, median **41.2730**
(−0.96% versus the production reference). First short-request warmup was
7.4561 s; subsequent short TTFT was 0.16–0.19 s. Zero restarts through the
probes, no ERROR/Traceback/HIP/HSA/OOM/NaN marker in the final run's logs, and
no amdgpu reset/fault in the node kernel journal during the experiment.

**Decision:** the 0.96 / 64K tuple passes this startup and HTTP smoke gate.
The original 0.94 fresh-cache tuple remains failed. A 64K needle test, tool
calling, cancellation/concurrency soak, the 128K workhorse envelope, and
other lanes' LoRA envelopes remain unqualified. Do not bulk-promote the five
active pins from this smoke result.

## Cleanup and next step

The canary parent Model was deleted after the final probes. Production cache
config hashes and mtimes were unchanged on both cards; the XTX fallback stayed
Ready with zero restarts throughout. The restored workhorse pod
`qwen38-27b-autoround-workhorse-7c464c5cdb-hbw7t` reached Ready with zero restarts
on its original image:
`registry.harbor.lan/flexinfer/runtime@sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`.
A proxy request completed at 00:08:17Z on September 15, returning `4` for
`2+2`, with fingerprint `vllm-0.23.0-203f4341`. Both production Models are
Ready/Active, and no temporary serving workload remains.

The node-local canary artifact and its isolated compile caches remain available
for the next qualification. Raw local evidence is under
`.loom/local/runtime-s2-v026-2026-09-14/` in this worktree.

Next: qualify the intended production envelope on an isolated canary, starting
with the 64K XTX lane's actual workload contract. The 128K workhorse cannot
inherit this result: even the warm 0.96 run's 129,615-token pool is short of
131,072, and the fresh-cache pool is only 72,817.
