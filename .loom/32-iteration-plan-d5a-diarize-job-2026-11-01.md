# D5-a meeting pipeline: diarization as a job on the Radeon VII (window plan, KT-DIAR)

Feature D5 (a), `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.5.
Matrix row `gfx906-diarize-job-2026-11-xx` in `60-validation-matrix.md` (pass rule written
2026-11-01, before any run). **Not yet run.** Everything merged so far is live-neutral.

| MR | What | State |
|---|---|---|
| !1244 | `pyannote` backend, `build/server-pyannote.py`, `build/Dockerfile.runtime-gfx906-pyannote` (built 2026-11-02 after the !1259 fix), GPUProfile certificate `pyannote/speaker-diarization` gating the backend, `deploy/debug/pyannote-diarization-radeonvii.yaml` | merged `6a335596` |
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

## Window as prepared 2026-11-02 (replaces W1–W6; W7–W8 amended)

Prepared by the agent on 2026-11-02, with nothing live applied. The layer is built and in Harbor,
and every live step is a merge request the owner merges. The certificate goes through git instead
of the W5 kubectl annotation, so Flux stays on: `flexinfer-models` applies `./deploy` (models and
gpuprofiles) with `prune: true`, so reverting the Model MR removes the lane, the certificate and
the annotation in one step. The certificate does **not** ride on the repin. A complete triple
renders as `certified` in `docs/certification-matrix.json`, so it lands only with the lane it
admits, in the window, with evidence that names this row as provisional.

**Layer (W1, done).** Built on the `7900xtx` host's CPU from !1259 (`635c80ce`). The first build,
from master `eed95f97`, failed the Dockerfile's own import gate: torchmetrics imports the runtime's
transformers 5, which the venv's huggingface_hub 0.25.2 cannot satisfy. !1259 shadows it in the
venv with transformers 4.46.3. Base
`runtime@sha256:6fc6fcbd…` (`rocm-gfx906-5ca58769`, the live gfx906 runtime with whisper.cpp),
and pushed with the host's own Harbor login:

- `NEW=registry.harbor.lan/flexinfer/runtime@sha256:cb4c8dc3c2bd55e0a5935642ed71b06d6fa1236ddd00f15b73627c1660359bc5`
- tags `rocm-gfx906-pyannote-6fc6fcbd9361`, `pinned-cb4c8dc3c2bd`; 7.689 GB compressed in Harbor (base 7.601 GB); the venv layer is 290 MB on disk, about 88 MB compressed
- Verified read-only on the build host: the venv imports torchaudio_compat, torch 2.9.0a0+git0fabc3b (the base's HIP fork, HIP 6.3.42134; no torch in the venv), pyannote.audio 3.3.2, huggingface_hub 0.25.2 and transformers 4.46.3; `SpeakerDiarization` and `server-pyannote.py` load; the runtime python (transformers 5.20.2, huggingface_hub 2.3.0, diffusers 0.42.0) and flexinfer-runtime, llama-server and whisper-server are byte-identical to the base.

| Step | MR | Owner go? | Live effect |
|---|---|---|---|
| P. prewarm `${NEW}` on the VII | !1260 | no (auto-merge) | the prewarm DaemonSet pulls the pyannote delta; nothing restarts |
| G1. jobs API on | !1255 (Draft) | **yes** | proxy restarts; `background: true` and `/v1/jobs*` live; imagegen-ui video goes async |
| G2. repin gfx906 runtime + HF token mount + `pyannote` bundled | !1261 (Draft) | **yes, quiet hour** | the VII runtime pod restarts; warm lanes reload in ~80 s; gte fails over to its CPU twin meanwhile |
| G3. lane + provisional certificate + `diarization-model` annotation, then KT-DIAR | the lane MR, branch `ops/pyannote-lane-kt-diar` (Draft, stacked on G2) | **yes, KT-DIAR window** | `pyannote-diarization-radeonvii` appears Idle (scale-to-zero); `diarize=true` jobs run |
| G4. verdict | new MR after the run | **yes** | PASS: evidence → the row, verdict; FAIL: revert G3 |

G1 and G2 are independent of each other. G3 needs both, and needs P's image resident.

```bash
export KUBECONFIG=/Users/cblevins/workspace/platform/gitops/.kube/k3s.yaml
NEW=registry.harbor.lan/flexinfer/runtime@sha256:cb4c8dc3c2bd55e0a5935642ed71b06d6fa1236ddd00f15b73627c1660359bc5
```

**P (after the prewarm MR merges).** `flux reconcile hr flexinfer -n flexinfer-system --with-source`, then:

```bash
kubectl -n flexinfer-system rollout status ds/flexinfer-image-prewarm-radeonvii-gfx906 --timeout=30m
ssh cblevins-radeonvii "sudo crictl images --digests | grep ${NEW##*sha256:} | head -c 200"   # first 12 hex chars appear
```

**G1 (owner go).** Merge !1255; smoke per its description (`GET /v1/jobs` lists, a
`background: true` chat completes). Rollback: revert !1255.

**G2 (owner go, quiet hour).** Merge the repin MR, `flux reconcile hr flexinfer -n
flexinfer-system --with-source`, then:

```bash
kubectl -n flexinfer-system rollout status ds/flexinfer-runtime-gfx906 --timeout=15m
kubectl -n flexinfer-system get models -o wide | grep radeonvii      # warm set back to Ready
kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- /opt/pyannote/bin/python -c "import torchaudio_compat, torch, pyannote.audio; print(torch.__version__, torch.version.hip, torch.cuda.is_available(), torch.cuda.get_device_name(0), pyannote.audio.__version__)"
kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- test -r /var/run/flexinfer/secrets/flexinfer-hf-token/HF_TOKEN && echo token-mounted
```

Expect `2.9.0… 6.3… True AMD Radeon VII 3.3.2` (run from `/opt/flexinfer`, where the shim lives:
add `-w /opt/flexinfer` if `kubectl exec` supports it, else `sh -c 'cd /opt/flexinfer && …'`) and
`token-mounted`. Rollback: revert the repin MR (the VII returns to `6fc6fcbd`, still prewarmed;
revert G3 first if it merged).

**G3 (owner go, KT-DIAR window, owner present).** Merge the Model MR (after G2 merged, GitLab
retargets it to master), reconcile, then:

```bash
flux reconcile kustomization flexinfer-models -n flux-system --with-source
kubectl -n flexinfer-system get model pyannote-diarization-radeonvii -o jsonpath='{.status.conditions[?(@.type=="BackendCertificate")].status} {.status.phase}{"\n"}'
ssh cblevins-radeonvii 'curl -s http://10.43.49.107/v1/models/whisper-large-v3-turbo-radeonvii' | python3 -c 'import json,sys; print(json.load(sys.stdin).get("flexinfer",{}).get("diarization"))'
```

Expect `True Idle`, then `{"available": true, "model": "pyannote-diarization-radeonvii", ...}`.
Warm once with a short clip. The first load downloads the gated weights, about 50 MiB. KT-ASR's
`twospk.wav` or the first 60 s of the fixture below both work:

```bash
ssh cblevins-radeonvii 'cd ~/kt-diar && curl -s http://10.43.49.107/v1/audio/transcriptions -F model=whisper-large-v3-turbo-radeonvii -F diarize=true -F num_speakers=2 -F file=@warm-60s.wav'
# -> {"object":"job","id":"job_…","status":"queued",…}; poll it:
ssh cblevins-radeonvii 'curl -s http://10.43.49.107/v1/jobs/<id>'          # status completed
ssh cblevins-radeonvii 'curl -s http://10.43.49.107/v1/jobs/<id>/content' | head -c 600
```

**W7 amended (KT-DIAR).** The nodes have no cluster DNS (`getent hosts
flexinfer-proxy.flexinfer-system.svc.cluster.local` fails on `cblevins-radeonvii` and
`cblevins-5930k`), so the harness takes ClusterIPs, as KT-RET did. Proxy `10.43.49.107`, gte
`10.43.153.107:8000`, bge-large `10.43.47.6:8000`, standalone pyannote `10.43.139.54:8000`; re-read
them with `kubectl -n flexinfer-system get svc` on the day. Run it on `cblevins-radeonvii` itself:
`--kfd` needs the host sysfs, the node has python3 and 102 GB free on `/`, and neither node has
ffmpeg, so the clip is cut with the stdlib.

Fixture (not committed; CC BY 4.0, AMI Meeting Corpus, University of Edinburgh mirror):
`EN2002a.Mix-Headset.wav` is 68,566,744 bytes, 16 kHz mono s16le, 35.7 min, 4 speakers. The
reference is pyannote's AMI-diarization-setup `only_words/rttms/test/EN2002a.rttm`, 746 turns,
speakers FEO070, FEO072, MEE071, MEE073.

```bash
scp scripts/kt-diar.py cblevins-radeonvii:~/kt-diar/   # mkdir -p ~/kt-diar first
ssh cblevins-radeonvii 'cd ~/kt-diar && \
  curl -sfLO https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/EN2002a/audio/EN2002a.Mix-Headset.wav && \
  curl -sfLO https://raw.githubusercontent.com/pyannote/AMI-diarization-setup/main/only_words/rttms/test/EN2002a.rttm && \
  python3 -I -c "
import wave
src = wave.open(\"EN2002a.Mix-Headset.wav\")
assert (src.getnchannels(), src.getframerate(), src.getsampwidth()) == (1, 16000, 2)
for name, secs in ((\"en2002a-30m.wav\", 1800), (\"warm-60s.wav\", 60)):
    src.rewind()
    out = wave.open(name, \"wb\"); out.setparams(src.getparams()); out.writeframes(src.readframes(16000 * secs)); out.close()
print(\"ok\")"'
```

Run (owner present; gte p95 baseline is the harness's own 240 s probe; 2026-11-02 reference
through the proxy: p95 24.7 ms at 3.8 req/s over 30 min):

```bash
ssh cblevins-radeonvii 'cd ~/kt-diar && sudo python3 -I kt-diar.py --audio en2002a-30m.wav --num-speakers 4 \
  --proxy http://10.43.49.107 \
  --gte-url http://10.43.153.107:8000/v1/embeddings --bge-url http://10.43.47.6:8000/v1/embeddings \
  --standalone http://10.43.139.54:8000/diarize \
  --kfd /sys/class/kfd/kfd/proc --out kt-diar-$(date -u +%Y%m%d) \
  --reference EN2002a.rttm --uri EN2002a --ref-offset 0 2>&1 | tee kt-diar.log'
```

`sudo` is only for the KFD queue `type` files. The harness counts compute queues only; SDMA queues
are not part of the 24-queue HWS limit. The task's `ls -d …/queues/* | wc -l` counts SDMA too
(20 on 2026-11-02 = 18 compute + the standalone Deployment's 2 SDMA). Spot check during the run:

```bash
ssh cblevins-radeonvii 'n=0; for t in /sys/class/kfd/kfd/proc/*/queues/*/type; do [ "$(sudo cat $t)" = 0 ] && n=$((n+1)); done; echo compute=$n'   # must stay <= 24
```

Then copy `kt-diar-<date>/` back and run the printed gate from the repo root:

```bash
scp -r cblevins-radeonvii:~/kt-diar/kt-diar-<date> . && scp cblevins-radeonvii:~/kt-diar/EN2002a.rttm .
go run ./cmd/flexinfer-bench diarize_conformance --reference EN2002a.rttm --uri EN2002a --ref-offset 0 \
  --hypothesis kt-diar-<date>/job.json --baseline kt-diar-<date>/baseline.json \
  --gte-baseline-p95-ms <printed> --gte-run-p95-ms <printed> --job <printed> \
  --image "${NEW}" --row gfx906-diarize-job-<date> --out kt-diar-report.json
```

Window telemetry. The plan's `node=` selector above returns nothing: `flexinfer_runtime_hw_queues`
carries `gpu_arch`/`pod`/`source`, not `node`. Use the loom Prometheus/Loki MCP tools or Grafana:

```promql
max_over_time(flexinfer_runtime_hw_queues{gpu_arch="gfx906",source="observed"}[1h])
histogram_quantile(0.95, sum by (le) (rate(flexinfer_proxy_request_duration_seconds_bucket{model="gte-qwen2-1p5b-radeonvii"}[5m])))
sum(rate(flexinfer_proxy_request_duration_seconds_count{model="gte-qwen2-1p5b-radeonvii"}[5m]))
```

```logql
{job="node-kernel"} |= "oversubscribed"
```

pyannote process VRAM for the evidence: `kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- curl -s localhost:8080/api/v1/models`, or the agent's `flexinfer_gpu_vram_*` series for the card, before and after the lane loads.

**Window hazards.** `gemma4-e4b-radeonvii` (priority 110) and
`openjev-0p8b-crossencoder-radeonvii` (112) outrank the lane (109) and are Idle. Activating either
during the run pushes the estimate past 21 queues and evicts the lowest lanes, the pyannote lane
first. Keep them idle for the window. A Humanizer burst (130) evicts pyannote by design.

**Queue budget and the standalone Deployment.** Measured 2026-11-02 18:10Z from
`/sys/class/kfd/kfd/proc`: 18 compute queues. That is bge-large, bge-reranker, gte, orpheus and
whisper.cpp at 3 each, plus the standalone pyannote Deployment's 3 (and 2 SDMA). Model estimates:
15 of 21 (24 − `reservedHWQueues 3`). With the lane: 18 of 21 estimated, 21 of 24 compute
measured. The Deployment and `reservedHWQueues: 3` both stay through KT-DIAR: the Deployment is
the DER baseline, and the reservation is what keeps the election from spending its queues.
Retiring them is §"After a PASS" 2: one MR drops `deploy/system/pyannote-diarization` and sets
`reservedHWQueues: 0` together, after ICC and jobsearch move to `diarize=true`.

**Rollbacks (as prepared).**

| Step | Rollback | Effect |
|---|---|---|
| G3 | revert the Model MR, `flux reconcile kustomization flexinfer-models -n flux-system --with-source` | prune removes the lane; the ASR annotation and the certificate go with it; `diarize=true` → 501 `diarization_not_certified` |
| G2 | revert the repin MR (G3 first), `flux reconcile hr flexinfer -n flexinfer-system --with-source` | the VII returns to `6fc6fcbd` (prewarmed, seconds); the token is unmounted |
| G1 | revert !1255 (or `proxy.jobs.enabled: false`), reconcile the HelmRelease | the proxy restarts with jobs off; imagegen-ui falls back to synchronous video |
| P | revert the prewarm MR | the prewarm DaemonSet drops `${NEW}` |

## Window steps (owner paste sequence, 2026-11-01 original)

W1–W6 are superseded by "Window as prepared 2026-11-02" above (layer built, certificate via git
instead of a Flux suspend and kubectl annotate). W7–W8 stand, amended there for node shells.

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

For the prepared window, the rollbacks at the end of "Window as prepared 2026-11-02" apply
(reverts through Flux); R5–R6 below describe the superseded kubectl path.

| Step | Rollback | Effect |
|---|---|---|
| R6 (lane, annotation) | `kubectl -n flexinfer-system delete model pyannote-diarization-radeonvii` and `kubectl -n flexinfer-system annotate model whisper-large-v3-turbo-radeonvii flexinfer.ai/diarization-model-` | `diarize=true` answers 501 `diarization_not_certified`; plain transcriptions untouched |
| R5 (provisional certificate) | `kubectl -n flexinfer-system annotate gpuprofile gfx906 flexinfer.ai/backend-certificate-pyannote-speaker-diarization- flexinfer.ai/backend-certificate-pyannote-speaker-diarization-since- flexinfer.ai/backend-certificate-pyannote-speaker-diarization-evidence-` then `flux resume kustomization flexinfer-models -n flux-system` | the controller refuses pyannote again (a live lane goes Failed) |
| R3 (runtime repin, token mount) | `git revert -m 1 <W3 merge sha>` in a revert MR, then `flux reconcile hr flexinfer -n flexinfer-system --with-source` | the VII rolls back to `${BASE}`; the token is unmounted |
| R2 (prewarm) | revert the W2 MR | the prewarm DaemonSet drops `${NEW}` |

The standalone `pyannote-diarization` Deployment and the proxy's `/diarize` route serve
throughout; nothing in this plan touches them.

## Budget during the window

Superseded by the measured budget in "Window as prepared 2026-11-02" (`reservedHWQueues` is 3
since !1207, so the Models' limit is 21, not 22).

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
