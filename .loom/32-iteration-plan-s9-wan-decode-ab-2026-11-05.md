# S9: the Wan decode A/B on the 0.32 base, the VACE VAE kill-test and a denoise cache (2026-11-05)

Status: **pre-registered 2026-11-05 ~23:00Z; no run yet.** The pass rules are the three matrix rows below,
written before any data. The run needs the XTX held by a GPULease, which the owner applies.

| Row | Question | Gates |
|---|---|---|
| `gfx1100-wan-decode-ab-v032-2026-11-05` | Do Wan T2V, Wan VACE and Stable Audio run no slower, no larger and no worse on the XTX runtime base `527e2714` than on their dedicated images? | !1237 (T2V), !1238 (VACE), !1239 (Stable Audio): owner Q7 deferred them until this A/B |
| `gfx1100-wan-vace-vae-bypass-2026-11-05` | Can VACE's fp32 VAE leave MIOpen's naive Conv3d without losing quality? | an opt-in flag in `build/server-diffusers.py` |
| `gfx1100-wan-denoise-cache-2026-11-05` | Does First Block Cache or TaylorSeer cut Wan's denoise by ≥ 25 % inside a quality bar? | an opt-in flag in `build/server-diffusers.py` |

Sources: owner Q7 and runbook D5–D7 in `.loom/32-iteration-plan-owner-window-2026-11-06.md`; slice S9 and §3.3
of `.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md`; the 11-03/11-04 Wan decode work in
`.loom/70-result-kt-flex-flex-card-2026-11-01.md`.

## 1. Why an offline A/B, not the runbook's live D5

Runbook D5 judges in-pod Wan after merging !1237. Owner Q7 wants the evidence first. So this A/B runs both stacks
on the XTX with neither lane changed:

- **Arm A, today's stack.**
  - `diffusers-video@b1f98ad0`: torch 2.6.0, ROCm 6.4.1, MIOpen 3.4 (`libMIOpen.so.1.0.60401`), diffusers 0.36.0,
    transformers 4.48.0, Python 3.10.
  - It is `83cda206` (VACE's pin) plus the two server-script layers, so it is VACE's stack too.
  - Stable Audio's A arm is `diffusers@d87f75f8`: torch 2.3, ROCm 6.2, diffusers 0.36.0.
- **Arm B, the in-pod target.**
  - `runtime@527e2714`: torch 2.13.0, ROCm 7.2.3 (`libMIOpen.so.1.0.70203`), diffusers 0.42.0,
    transformers 5.19.0, Python 3.12.
- **Same server code.** Each arm runs its image's own `server-diffusers.py`. Both Wan images carry the same
  file, byte-identical to master `4da96d96` (md5 checked 2026-11-05). So the A/B isolates the stack:
  torch, ROCm, MIOpen and diffusers.

**How it runs.**
- `scripts/wan-decode-ab-host.sh` runs on the node and starts one `k3s ctr run` container per phase.
  - The image is already in containerd, so nothing is pulled.
  - The container gets `/dev/kfd` and `/dev/dri`, and the lane's Local cache read-only at `/models`.
  - It gets a fresh compile/MIOpen cache per arm, and the lane's rendered env (the Deployment env from
    `kubectl get deploy`).
  - Limits mirror deployment. A gets 8 CPUs and 48 GiB, like the Deployment. Stable Audio A gets 6 CPUs and 16 GiB.
    B gets 48 GiB and no CPU quota, like the runtime pod.
  - B also gets the runtime pod's `PYTORCH_ALLOC_CONF`, and the image's `runtime.env` through its entrypoint.
- `scripts/wan-decode-ab.py run` loads the pipeline through the server's own `load_pipeline`. It renders each clip
  with the call `/v1/videos/generations` makes, including the same `[video-timing]` fields. It also saves:
  - the final latents;
  - uint8 frames;
  - the MP4;
  - torch memory peaks per phase and the process peak RSS.
- A 5 Hz sampler reads the card's `mem_info_vram_used` (card0 = 0000:03:00.0, the Navi 31).

**What it cannot see.**
- The proxy and the runtime manager's load request. The live D5–D7 checks after a merge still cover those.
- A 5-minute first byte through the proxy. Bar 4 stands in for it: process start to the first clip, with the
  compile cache warm.

## 2. Requests (fixed before the run)

| Id | Lane | Prompt | Seed | Shape |
|---|---|---|---|---|
| `boat` | T2V, VACE | "a paper boat drifting down a rain gutter, shallow depth of field" (KT-FLEX / KT-JOBS) | 42 | 832x480, 33 frames, 20 steps, guidance 5.0, lane negative prompt |
| `fox` | T2V | "a red fox trotting through fresh snow at dusk, cinematic lighting" | 7 | same |
| VACE keyframe | VACE | frame 0 of arm A's T2V `c1` clip, saved as `t2v-A/keyframe.png` and used by every VACE arm | 42 | same |
| `rain` | Stable Audio | "gentle rain on a tin roof with distant thunder" | 42 | 20 s (the server default), 100 steps |

## 3. Phases (order is priority; ~2 h 30 min in all)

| Phase | Image / server | Renders | Est. |
|---|---|---|---|
| `t2v-A` | A, image server, empty cache | `c0` boat (first-ever cold), `c1` boat (warm), `c2` fox (warm), decode-only of its own `c1` latents (`own`, validity) | 12 min |
| `t2v-A-cold` | A, cache from `t2v-A` | `k` boat in a fresh process (cold clip) | 4 min |
| `t2v-B` | B, image server, empty cache | `c0`, `c1`, `c2` as A, decode-only of **A's** `c1` latents (`xA`, decode equivalence) | 12 min |
| `t2v-B-cold` | B, cache from `t2v-B` | `k` | 4 min |
| `vace-A` / `vace-B` | A / B, image server | `v0` (cold, recorded), `v1` (warm, compared), keyframe above | 20–25 min each |
| `vace-B-bf16` | B, **branch** server, `WAN_VAE_DECODE_DTYPE=bfloat16` | `v1`, decode-only of `vace-B`'s `v1` latents (`x0`) | 8 min |
| `vace-B-fp32` | B, branch server, `WAN_VAE_MIOPEN_BYPASS=fp32` | same | 8 min |
| `sa-A` / `sa-B` | SA image / B | `a0` (cold), `a1` (warm) | 5 min each |
| `cache-B-fbc005`, `cache-B-ts541` | B, branch server, `WAN_DENOISE_CACHE=fbc:0.05` / `taylorseer:5:4:1` | `c0`, `c1`, `c2` | 8 min each |
| `cache-B-fbc010` | same, `fbc:0.1` | only if `fbc005` passes C2 | 8 min |

The branch server is `build/server-diffusers.py` from branch `feat/s9-wan-opt-in-flags` (Draft MR; each run
records the file's sha256). Its new flags are off by default, and the A/B phases do not use it.

## 4. Pre-checks, the owner's one command, the run

1. **XTX free.**
   - S7's pool canary is deleted and its note says "XTX free".
   - No other debug canary is on `7900xtx-textgen`, and no Model there at priority ≥ 480 is anything but Idle.
     The creative lanes at 500 are Idle and expected.
   - No GPULease exists.

   ```bash
   kubectl get models.ai.flexinfer -n flexinfer-system -o json | jq -r '.items[] | select(.spec.gpu.shared=="7900xtx-textgen" and .spec.gpu.priority >= 480) | "\(.metadata.name) \(.spec.gpu.priority) \(.status.phase)"'
   kubectl get gpuleases.ai.flexinfer -A
   ```

   The card link is Gen3 x8, and the kernel log is quiet (runbook phase D).
2. **Owner: hold the card** for 4 h. Every member of `7900xtx-textgen` parks, including the warm lane: the
   5930k workhorse's overflow target is down while the lease is held.

   ```bash
   kubectl apply -f - <<EOF
   apiVersion: ai.flexinfer/v1alpha2
   kind: GPULease
   metadata:
     name: s9-wan-decode-ab
     namespace: flexinfer-system
   spec:
     group: 7900xtx-textgen
     node: cblevins-7900xtx
     owner: s9-wan-decode-ab
     expiresAt: "$(date -u -v+4H +%Y-%m-%dT%H:%M:%SZ)"
   EOF
   ```

3. **Run.**
   - Wait until the card reads < 2 GiB used. Every phase checks this itself and skips if the card is busy.
   - Copy `scripts/wan-decode-ab.py`, `scripts/wan-decode-ab-host.sh` and `build/server-diffusers.py` to
     `~/wan-ab-2026-11-05/code/` on the node. `/home` is its own NVMe, 433 GB free.
   - Start the run:

     ```bash
     ssh cblevins-7900xtx 'nohup bash ~/wan-ab-2026-11-05/code/wan-decode-ab-host.sh ~/wan-ab-2026-11-05 t2v-A t2v-A-cold t2v-B t2v-B-cold vace-A vace-B vace-B-bf16 vace-B-fp32 sa-A sa-B cache-B-fbc005 cache-B-ts541 >~/wan-ab-2026-11-05/host.log 2>&1 &'
     ```

   - Watch `phases.log` with short ssh polls.
   - `smoke` (no GPU, no lease) checked the mounts, env file, entrypoints and server imports on all three images
     on 2026-11-05 22:49Z.
4. **Collect.**
   - `rsync` the root back without `cache/`.
   - Run `python3 scripts/wan-decode-ab.py collect <root>`. It writes `host.json`, `sims.json` and `verdicts.json`.
   - PSNR/SSIM are numpy (BT.601 luma SSIM, 11x11 Gaussian, σ 1.5). The H.264 check is
     `ffprobe -show_entries stream=codec_name,nb_frames`.
5. **Hand back.**
   - The owner deletes the lease (`kubectl delete gpulease -n flexinfer-system s9-wan-decode-ab`), or it expires.
   - One proxied warm-lane request reclaims the card (runbook phase D hand-back ping).
   - Write "XTX free" in the shared notes.

## 5. After the verdicts

- **Row 1, T2V `pass`.**
  - Refresh !1237 with the evidence and retitle it "ready for an owner window".
  - The runbook's live D5 checks stay the promotion gate after the merge.
- **Row 1, T2V `conditional`** (bar 10 alone missed): the owner looks at the MP4s side by side.
- **Row 1, T2V `fail`:** !1237 stays deferred, and the result names the bar.
- **VACE and Stable Audio** are judged on their own bars and move !1238 and !1239 the same way.
- **Rows 2 and 3.**
  - A passing setting ships as its opt-in flag, default off, in `build/server-diffusers.py`, with tests.
  - Turning it on for a lane is a separate live change for an owner window.
  - A failing setting's code is dropped from the MR.
- **Write-up.** `.loom/70-result-s9-wan-decode-ab-2026-11-05.md` and the rows' `promotion_decision`.
