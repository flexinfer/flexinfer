# Result: D7 efficiency and evidence as product (2026-11-01)

Feature D7 of `10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.7. Status:
**code merged, default-off; enablement is a Draft for the owner; the site page is a Draft.**

| MR | Repo | State | What |
|---|---|---|---|
| !1225 | services/flexinfer | merged (bd06864f) | `flexinfer_gpu_power_watts{gpu,node}` from sysfs hwmon, off by default; agent flag-parse fix |
| !1230 | services/flexinfer | merged (c36b5f2a) | `flexinfer_lane_energy_joules_total{model,node,gpu}`, tokens-per-joule and power panels |
| !1228 | services/flexinfer | merged (85911b49) | `flexinfer certify render`, `docs/certification-matrix.json`, `make certify-matrix`, CI `certify_matrix` |
| !1231 | services/flexinfer | **Draft, owner rolls** | `agent.gpuPower.nodes: [cblevins-5930k, cblevins-radeonvii]` |
| !187 | services/flexinfer-site | **Draft, not merged** | `/products/flexinfer/serving-matrix` page + evidence-derived product copy |

## What is measured

- **`flexinfer_gpu_power_watts{gpu,node}`**: board power from the amdgpu hwmon attribute
  `power1_average` (fallback `power1_input`), microwatts → watts, every `--gpu-power-interval`
  (default 1 s, floor 500 ms). Sysfs reads only; `rocm-smi` is never called on this path. `gpu` is
  the DRM card index, the same as `flexinfer_gpu_healthy` (the 5930k's card is `card1` in sysfs
  and `0` in rocm-smi, so join with temperature on `node`).
- **`flexinfer_gpu_power_sampler_active{node}`**: 1 while sampling, 0 after a self-stop.
- **`flexinfer_lane_energy_joules_total{model,node,gpu}`**: the integral, attributed to lanes.
- Off by default: a node samples only when named in `--gpu-power-nodes` (chart
  `agent.gpuPower.nodes`, no wildcard). With the empty default the chart renders the agent
  DaemonSet and RBAC byte-identical to before (checked with `helm template` against
  `deploy/system/values-k3s.yaml`), so merging the code rolled nothing.
- Safety for the riskiest assumption (1 Hz SMU-backed reads are harmless on ROCm 7.2): the
  sampler requires the health sentinel, skips a runtime-suspended card, and **latches itself off**
  until the agent restarts on a degraded sentinel verdict (including a hard sysfs verdict found
  mid-cycle), on the first device I/O error (`EIO`/`EREMOTEIO`/`ENXIO`/`ENODEV`), or after 5
  consecutive failed reads (missing, unparsable, above 1000 W). It deletes its power series and
  logs `GPU power sampling stopped` with the reason. The sentinel already read `power1_average`
  once per 30 s probe; enabling the sampler raises that to 1 Hz and adds nothing else.
- Not measured: cblevins-7900xtx (stays off until its soak verdict, 2026-11-06 00:15Z),
  NVIDIA cards (no hwmon power path), whole-node power.

## Attribution rule

Between two consecutive good samples of a card the agent adds `(P_prev + P_now) / 2 × Δt`
joules and splits them **equally** among the lanes Active on the card at the later sample. A
lane is Active when its `Model` (agent's namespace) is phase `Ready`, `status.gpu.node` is the
node, and it allocates a GPU; CPU-only lanes on the node do not count. No Active lane: the
energy is attributed to nobody. Steps longer than 5 periods (stall, suspended card, failed read)
are not integrated; Loading and Preempted time is not attributed. The lane set is re-listed
every 15 s off the sampling goroutine, and three failed listings clear it. A node with more
than one sampled card attributes nothing (the agent cannot tell which card a lane holds); every
fleet node has one.

Consequence on the shared Radeon VII: bge-large, bge-reranker, gte and Orpheus each get a
quarter of the draw although gte carries ~115K of the card's ~119K requests a day. Tokens per
joule there is a head-count number, not a work number. A busy-time or request-weighted split is
the obvious refinement (follow-up 4).

Tokens per joule (Mission Control, "Tokens per Joule by Lane") divides the proxy's
`flexinfer_proxy_request_completion_tokens_sum` (chat lanes) and
`flexinfer_proxy_prompt_tokens_total` (embedding and rerank lanes) rates by the lane's energy
rate. Streaming requests that end without a usage chunk are not counted.

## What the matrix contains

`docs/certification-matrix.json` (`flexinfer.ai/certification-matrix/v1`, 183 entries at
!1228; 184 with this note's pending row): 3 GPU classes (gfx1100 RX 7900 XTX on cblevins-5930k + cblevins-7900xtx, gfx906
Radeon VII, sm_52 GTX 980 Ti), and entries of four kinds, each with `support_level`, backend,
GPU class, `evidenced`, evidence pointer and date:

- 20 declared backend postures, 5 of them backed by a validation verdict;
- 5 certified backend capabilities: gfx1100 llama.cpp `draft-mtp`, vLLM `qwen35-moe-mtp1`;
  gfx906 llama.cpp `stateful-slots`, `ngram-simple-draft16-min1`, vLLM `qwen35-dense-mtp1`;
- 76 lanes, 31 evidenced (a verdict row names the lane, or it carries promotion evidence);
- 82 validation rows (the Promotion Matrix table), 67 with a verdict.

Claims on the ROCm cards today:

| Workload | Level | Where | Behind it |
|---|---|---|---|
| chat | supported | gfx1100, gfx906 | 17 evidenced production lanes, newest 2026-10-27 |
| embeddings | supported | gfx906 | bge-large, gte (2026-10-24) |
| rerank | supported | gfx906 | bge-reranker (2026-10-23) |
| TTS | supported | gfx906, gfx1100 | Orpheus on the VII, SNAC decode on the XTX profile (2026-10-23) |
| image | experimental | gfx906 | sdxl-inpainting-radeonvii, conditional (2026-05-22) |
| ASR | **cannot claim** | | no Promotion Matrix row names an ASR lane; KT-ASR's 34.7× verdict lives only in the voice-stack doc and !1207 is unmerged |
| video | **cannot claim** | | wan21 is named only by the pending KT-JOBS row |
| vision | **cannot claim** | | no verdict row names a vision lane |
| RAG | **cannot claim** | | nothing names `/v1/rag`; it is a proxy read path, not a lane |

45 of 76 lanes are unverified, among them Qwen3-Embedding-4B and Qwen3-Reranker-0.6B, whose
2026-10-24 row (`qwen3-embedding-gfx906-2026-10-24`, promote) names the models but not the lane
names.

## What the matrix omits

Live state (a lane in git may be parked or Idle; `--cluster` mode is not implemented), energy
and throughput numbers, evidence outside the Promotion Matrix table unless a row, certificate or
annotation points at it, and anything about cblevins-7900xtx's current stability beyond the
soak rows. A row whose `gpu_class` cell names two architectures (this note's own row, the
`GPUProfile` row) is recorded under the first one only. The pass rule for enabling the sampler is recorded as the pending row
`gpu-power-sampling-1hz-2026-11-01`.

## Owner decisions

1. Roll !1231 (5930k + Radeon VII) once its prerequisites hold: !1225 and !1230 merged and the
   master pipeline after them published `flexinfer-agent:master`. Watch list and off switch are
   in the MR and `docs/agents/operations.md#gpu-power-sampling`.
2. cblevins-7900xtx: after the soak verdict, decide whether to list it. Nothing here enables it.
3. Review and publish the site Draft (or not); it shows internal evidence paths through the
   public GitHub mirror and short image digests, no registry hostnames.
4. Whether the equal split is good enough for public tokens-per-joule claims on the shared VII.

## Follow-ups

1. platform/gitops fleet-utilization dashboard
   (`k3s/monitoring/dashboards/services-flexinfer-fleet-utilization-dashboard.yaml`): add a
   per-node power panel (`sum by (node) (flexinfer_gpu_power_watts)`) and a per-lane
   tokens-per-joule panel (the Mission Control queries). Not touched this round.
2. Matrix rows that would let the matrix claim more: a KT-ASR row naming the whisper.cpp lane
   once !1207 is applied; a wan21 video verdict row; a `/v1/rag` read-path row; name lanes by
   their Model names in rows (Qwen3-Embedding-4B / Reranker).
3. A `FlexInferGPUPowerSamplerStopped` alert on `flexinfer_gpu_power_sampler_active == 0`.
4. Busy-time or request-weighted attribution for shared cards.
5. `flexinfer certify render --cluster` reading live Models/GPUProfiles; a public endpoint for
   the matrix instead of the site's checked-in copy.
6. Pin the agent image by digest in `values-k3s.yaml` (it runs the mutable `master` tag).
