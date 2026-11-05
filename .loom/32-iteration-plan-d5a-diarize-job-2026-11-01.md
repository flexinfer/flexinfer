# D5-a meeting pipeline: diarization as a job on the Radeon VII (window plan, KT-DIAR)

Feature D5 (a), `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.5.
Matrix row `gfx906-diarize-job-2026-11-xx` in `60-validation-matrix.md` (pass rule written
2026-11-01, before any run). **Not yet run.** Everything merged so far is live-neutral.

| MR | What | State |
|---|---|---|
| !1244 | `pyannote` backend, `build/server-pyannote.py`, `build/Dockerfile.runtime-gfx906-pyannote` (not built), GPUProfile certificate `pyannote/speaker-diarization` gating the backend, `deploy/debug/pyannote-diarization-radeonvii.yaml` | merged `6a335596` |
| !1245 | `diarize=true` on `/v1/audio/transcriptions` as a D2 job, `flexinfer.ai/diarization-model`, 501 `diarization_not_certified`, descriptor `flexinfer.diarization`, `pkg/transcript` speaker attribution | see the MR |
| D5-a MR 3 | `flexinfer-bench diarize_conformance` (DER vs a reference RTTM), `scripts/kt-diar.py`, this plan, the KT-DIAR row | see the MR |

## What is live-neutral, and what keeps it so

- No GPUProfile lists `pyannote` in `spec.runtime.bundledBackends` or carries the certificate, so
  the controller refuses any pyannote Model (`BackendCertificate=False`, `BackendNotCertified`,
  Phase `Failed`). `TestBackendCertificateDecisionUsesTheProfileCache` loads the shipped
  `deploy/gpuprofiles/gfx906.yaml` and asserts the refusal.
- No Model carries `flexinfer.ai/diarization-model`, so a `diarize=true` request answers 501
  (`jobs_disabled` while jobs are off, else `diarization_not_certified`). Without `diarize`, a
  transcription reaches the ASR lane with the client's bytes (`TestTranscriptionWithoutDiarize
  IsUnchanged`); the whisper.cpp ingress output is byte-identical to its pre-move golden.
- The image layer is a Dockerfile only; no manifest references it.

## Riskiest assumption and its kill-test

**pyannote fits the Radeon VII's queue and VRAM budget beside the warm set without moving gte's
p95.** The hand-written Deployment measured 2 compute queues (SDMA on) and ~1.65 GiB, and the card
peaked at 18 of 24 queues with it during KT-ASR; but whisper.cpp unmasked moved gte p95 +38 %
(KT-ASR run 1) and pyannote's segmentation and embedding passes are as compute-heavy per second
of audio. The lane is admitted at `hwQueueEstimate: 3`, 1,700 MB, `HSA_CU_MASK=0:0-39`.

**Kill-test: KT-DIAR itself.** Its gte criterion is the assumption's test: gte p95 during the job
within 20 % of the same window's 240 s baseline (KT-ASR's criterion d), 0 probe errors, the
card's observed compute queues ≤ 24 (`flexinfer_runtime_hw_queues{source="observed"}` and
`scripts/kt-diar.py --kfd`) with no `Runlist is getting oversubscribed` in the node kernel log,
and the pyannote process ≤ its 1,700 MB estimate. If gte moves, the first remedy to try is a
narrower CU mask on the pyannote lane (`HSA_CU_MASK=0:0-29`), then a re-run, before any other
change.

## Pass rule (§6.5 verbatim) and how the gate reads it

> a 30-min AMI meeting through the job path, speaker-attributed words, DER within 5 points of the
> standalone pyannote Deployment, gte p95 unaffected

`flexinfer-bench diarize_conformance` (`docs/user/gauntlet.md`) turns it into four checks, all of
which must pass:

| Check | Pass when |
|---|---|
| `meeting_length` | the scored clip is ≥ 29.5 min (AMI EN2002a Mix-Headset 0:00–30:00) |
| `speaker_attributed_words` | ≥ 95 % of the job's words carry a speaker |
| `der_vs_standalone` | DER(job `diarization.segments`) − DER(standalone `/diarize`) ≤ 5 points; both against the AMI `only_words` reference RTTM, collar 0.25 s each side, overlap scored, optimal speaker mapping; the words-derived DER is reported, not gated |
| `gte_p95` | gte p95 during the job ≤ 1.2 × the window's baseline p95 (from `scripts/kt-diar.py`) |

## Preconditions (agent-checkable, nothing applied)

1. The XTX soak verdict is in (2026-11-06 00:15Z): W1 builds on the `7900xtx` docker host.
2. !1206/!1207 applied: the whisper.cpp lane `whisper-large-v3-turbo-radeonvii` serves, on a
   gfx906 runtime with whisper.cpp. Read the digest the DaemonSet runs; that is W1's base.
3. D2 jobs on: the KT-JOBS owner actions (`32-iteration-plan-kt-jobs-2026-10-31.md`: MinIO bucket
   `flexinfer-jobs`, Secret `flexinfer-jobs-minio`, `proxy.jobs.enabled`).
4. Controller and proxy images built from a master containing !1244 and !1245, rolled.
5. Secret `flexinfer-hf-token` (key `HF_TOKEN`) in `flexinfer-system` belongs to an account that
   accepted `pyannote/segmentation-3.0` and `pyannote/speaker-diarization-3.1` (the Deployment's
   token already does).
6. Owner present, quiet hour. W3 rolls the gfx906 runtime pod: every VII lane reloads in-pod
   (seconds each); gte requests fail over only if D6's twin reroute (!1217) is live.

Owner decision **Q1**: roll the VII once by building the pyannote layer on the whisper.cpp runtime
digest *before* the !1206 roll and rolling straight to the layered image (one roll, both
features), or keep two rolls (simpler rollback). This plan assumes two.

## Window steps (owner paste sequence)

```bash
export KUBECONFIG=/Users/cblevins/workspace/platform/gitops/.kube/k3s.yaml
```

**W1. Build the pyannote layer** (repo root at master, 7900xtx docker host):

```bash
BASE="$(kubectl -n flexinfer-system get ds flexinfer-runtime-gfx906 -o jsonpath='{.spec.template.spec.containers[0].image}')"
echo "${BASE}"
B12="$(printf %s "${BASE##*sha256:}" | cut -c1-12)"
TAG="registry.harbor.lan/flexinfer/runtime:rocm-gfx906-pyannote-${B12}"
docker --context 7900xtx build -f build/Dockerfile.runtime-gfx906-pyannote --build-arg "RUNTIME_IMAGE=${BASE}" -t "${TAG}" .
docker --context 7900xtx push "${TAG}"
NEW="$(docker --context 7900xtx inspect --format '{{index .RepoDigests 0}}' "${TAG}")"
N12="$(printf %s "${NEW##*sha256:}" | cut -c1-12)"
docker --context 7900xtx tag "${TAG}" "registry.harbor.lan/flexinfer/runtime:pinned-${N12}"
docker --context 7900xtx push "registry.harbor.lan/flexinfer/runtime:pinned-${N12}"
echo "${NEW}"
```

The build's last `RUN` fails it if the import chain breaks, if torch is not the ROCm build, or if
the venv leaked `huggingface_hub 0.25.2` into the runtime's own python. The `pinned-` tag keeps
Harbor retention off the digest.

**W2. Prewarm on the VII** (MR, merged by the agent on request): add `${NEW}` to
`imagePrewarm.profiles[radeonvii-gfx906].images` in `deploy/system/values-k3s.yaml`, `make
certify-matrix`, commit `docs/certification-matrix.json`. Wait for
`flexinfer-image-prewarm-radeonvii-gfx906` to be Ready.

**W3. Repin the gfx906 runtime and mount the token** (one MR):

- `deploy/system/values-k3s.yaml`, `runtime.profiles[gfx906]`: `image: ${NEW}` and

  ```yaml
      extraVolumes:
        - name: hf-token
          secret:
            secretName: flexinfer-hf-token
            defaultMode: 0400
            items:
              - key: HF_TOKEN
                path: HF_TOKEN
      extraVolumeMounts:
        - name: hf-token
          mountPath: /var/run/flexinfer/secrets/flexinfer-hf-token
          readOnly: true
  ```

- `deploy/gpuprofiles/gfx906.yaml`: `spec.runtime.image: ${NEW}`, append `pyannote` to
  `spec.runtime.bundledBackends` (no certificate yet).
- `make certify-matrix`; commit the JSON. Merge, `flux reconcile hr flexinfer -n flexinfer-system
  --with-source`, wait for `flexinfer-runtime-gfx906` to roll and the warm set to be Ready.

**W4. Check the layer on the card:**

```bash
kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- /opt/pyannote/bin/python -c "import torch, pyannote.audio; print(torch.__version__, torch.version.hip, torch.cuda.is_available(), torch.cuda.get_device_name(0), pyannote.audio.__version__)"
kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- test -r /var/run/flexinfer/secrets/flexinfer-hf-token/HF_TOKEN && echo token-mounted
```

Expect `True AMD Radeon VII 3.3.2` and `token-mounted`.

**W5. Provisional certificate** (Flux suspended for the window; KT-DIAR needs the controller to
admit the lane, and the git certificate follows the verdict):

```bash
flux suspend kustomization flexinfer-models -n flux-system
kubectl -n flexinfer-system annotate gpuprofile gfx906 \
  flexinfer.ai/backend-certificate-pyannote-speaker-diarization="${NEW}" \
  flexinfer.ai/backend-certificate-pyannote-speaker-diarization-since="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  flexinfer.ai/backend-certificate-pyannote-speaker-diarization-evidence="provisional: KT-DIAR window, .loom/60-validation-matrix.md gfx906-diarize-job-2026-11-xx"
```

**W6. Apply the lane and point the ASR lane at it:**

```bash
kubectl apply -f deploy/debug/pyannote-diarization-radeonvii.yaml
kubectl -n flexinfer-system get model pyannote-diarization-radeonvii -o jsonpath='{.status.conditions[?(@.type=="BackendCertificate")].status} {.status.phase}{"\n"}'
kubectl -n flexinfer-system annotate model whisper-large-v3-turbo-radeonvii flexinfer.ai/diarization-model=pyannote-diarization-radeonvii
curl -s http://flexinfer-proxy.flexinfer-system.svc/v1/models/whisper-large-v3-turbo-radeonvii | jq .flexinfer.diarization
```

Expect `True`, then `{"available": true, "model": "pyannote-diarization-radeonvii", ...}`. Warm
the lane once (its first load downloads ~50 MiB of gated weights) with a short clip, e.g. KT-ASR's
`twospk.wav`:

```bash
curl -s http://flexinfer-proxy.flexinfer-system.svc/v1/audio/transcriptions \
  -F model=whisper-large-v3-turbo-radeonvii -F diarize=true -F num_speakers=2 -F file=@twospk.wav | jq '{id, status}'
```

**W7. KT-DIAR.** Fixture: AMI EN2002a Mix-Headset (CC BY 4.0), first 30 minutes, 16 kHz mono
(`ffmpeg -i EN2002a.Mix-Headset.wav -t 1800 -ac 1 -ar 16000 en2002a-30m.wav`); reference
`only_words/rttms/test/EN2002a.rttm` from `github.com/pyannote/AMI-diarization-setup`. From a
shell with cluster DNS (the `cblevins-5930k` node shell, as KT-RET ran; `--kfd` only on
`cblevins-radeonvii`):

```bash
python3 scripts/kt-diar.py --audio en2002a-30m.wav --num-speakers 4 --out kt-diar-$(date -u +%Y%m%d) \
  --reference EN2002a.rttm --uri EN2002a --ref-offset 0
```

It probes gte and bge for 240 s, submits the diarize job, polls it with the probe running,
stores `job.json`, then runs the same clip through the standalone Deployment (`baseline.json`)
and prints the gate command with the two gte p95s filled in. Run it (`go run
./cmd/flexinfer-bench diarize_conformance ... --image "${NEW}" --row
gfx906-diarize-job-<date> --out kt-diar-report.json`), and read the window's queue peak:

```promql
max_over_time(flexinfer_runtime_hw_queues{source="observed",node="cblevins-radeonvii"}[1h])
```

plus `{job="node-kernel"} |= "oversubscribed"` in Loki for the window.

**W8. Verdict.** Paste the gate's row over the pending one, with the queue peak and the pyannote
process VRAM in `validation_evidence`.

- **PASS:** one MR: the certificate triple in `deploy/gpuprofiles/gfx906.yaml` bound to
  `${NEW}` (evidence: the row), the lane moved to `deploy/models/` (and its kustomization), the
  `flexinfer.ai/diarization-model` annotation on the whisper.cpp Model manifest, the row's
  verdict, `make certify-matrix`. After it merges, drop the provisional annotations (R5 below)
  and `flux resume kustomization flexinfer-models -n flux-system`; Flux applies the git triple.
- **FAIL:** R6, then R5. The layered runtime may stay (it only adds a venv); the lane cannot load.

## Rollbacks (paste-ready)

| Step | Rollback | Effect |
|---|---|---|
| R6 (lane, annotation) | `kubectl -n flexinfer-system delete model pyannote-diarization-radeonvii` and `kubectl -n flexinfer-system annotate model whisper-large-v3-turbo-radeonvii flexinfer.ai/diarization-model-` | `diarize=true` answers 501 `diarization_not_certified`; plain transcriptions untouched |
| R5 (provisional certificate) | `kubectl -n flexinfer-system annotate gpuprofile gfx906 flexinfer.ai/backend-certificate-pyannote-speaker-diarization- flexinfer.ai/backend-certificate-pyannote-speaker-diarization-since- flexinfer.ai/backend-certificate-pyannote-speaker-diarization-evidence-` then `flux resume kustomization flexinfer-models -n flux-system` | the controller refuses pyannote again (a live lane goes Failed) |
| R3 (runtime repin, token mount) | `git revert -m 1 <W3 merge sha>` in a revert MR, then `flux reconcile hr flexinfer -n flexinfer-system --with-source` | the VII rolls back to `${BASE}`; the token is unmounted |
| R2 (prewarm) | revert the W2 MR | the prewarm DaemonSet drops `${NEW}` |

The standalone `pyannote-diarization` Deployment and the proxy's `/diarize` route serve
throughout; nothing in this plan touches them.

## Budget during the window

Warm set 12 queues + whisper.cpp 3 + the pyannote lane 3 = 18 of the Models' 22 (24 − 2 reserved
for the Deployment), and 20–21 of 24 with the Deployment's own 2–3. VRAM by estimate: 8,100 +
1,500 + 1,700 = 11,300 MB of 16,384 in the election, plus ~1,650 MB for the Deployment outside
it; live use has run ~35 % under the estimates. `TestHWQueueBoundAdmitsTheD5aVoiceSet` checks the
election admits this set with `reservedHWQueues` 2 and 0, and that a Humanizer burst evicts the
voice lanes, never gte.

## After a PASS (follow-ups, not in the window)

1. ICC (`ICC_ASR_DIARIZE_URL`) and jobsearch ("Diarization not supported by the ASR backend")
   repoint to `diarize=true` on the proxy, falling back on 501 `diarization_not_certified`.
2. Then retire the Deployment in one MR: drop `deploy/system/pyannote-diarization` from its
   kustomization and set `reservedHWQueues: 0` on gfx906 together, so its queues are not counted
   twice or dropped early.
3. §5.6 of the voice-stack doc: re-evaluate `speaker-diarization-community-1` (pyannote.audio 4)
   as a second certificate, with the same gate.

## Not tested before the window

The sandbox has no torch, so nothing that executes pyannote ran: the server's real load and
inference, `torchaudio_compat` under the venv, gfx906 kernels for the segmentation and embedding
models, the Dockerfile build and the venv's isolation from the runtime's `huggingface_hub`, the
mounted-token path in a real pod, DER numbers, and the lane's VRAM and queues. The Go side
(backend env, certificate gate, admission, proxy job pipeline, speaker attribution, DER) and the
server's request contract (stub-based `scripts/test_server_pyannote.py`) are tested.
