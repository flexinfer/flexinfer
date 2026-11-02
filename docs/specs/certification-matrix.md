---
title: Certification matrix
description: The generated JSON of what flexinfer serves per GPU class, at what support level, with the evidence behind it.
---

# Certification matrix

`docs/certification-matrix.json` is the machine-readable answer to "what does
flexinfer serve on which consumer GPU, how well, and how do we know". It is
generated, never edited by hand:

```bash
make certify-matrix                        # = go run ./cmd/flexinfer certify render
go run ./cmd/flexinfer certify render --check   # what CI runs
go run ./cmd/flexinfer certify render -o -      # print instead of write
```

It renders offline from four repository inputs, so CI needs no cluster:

| Input | Default path | What it contributes |
|---|---|---|
| GPUProfiles | `deploy/gpuprofiles/*.yaml` | GPU classes, declared backend posture (`spec.backends.<b>.support`), backend-capability certificates (`flexinfer.ai/backend-certificate-*`) |
| Lanes | `deploy/models/*.yaml` | one entry per `Model`: backend, node, modality, `flexinfer.ai/promotion-evidence` |
| Validation matrix | `.loom/60-validation-matrix.md` | the Promotion Matrix table, one entry per row |
| Node classes | `deploy/system/values-k3s.yaml` | `runtime.profiles[]` (`gpuArch` + hostname selector) maps nodes to GPU classes |

The CI job `certify_matrix` fails a merge request that changes any input (or
`pkg/certify`, `cmd/flexinfer`, `api/v1alpha2`, `.gitlab-ci.yml`) without the
regenerated JSON; on `master` it only warns. **If you add or edit a validation
matrix row, a GPUProfile annotation or a lane manifest, run `make
certify-matrix` and commit `docs/certification-matrix.json` with it.**

## Document

```json
{
  "schema": "flexinfer.ai/certification-matrix/v1",
  "sources": { "gpu_profiles": "...", "models": "...", "validation_matrix": "...", "node_classes": "..." },
  "rules": ["...the derivation rules below, as text..."],
  "gpu_classes": [ { "class": "gfx1100", "vendor": "amd", "product": "AMD Radeon RX 7900 XTX", "vram_mb": 24576, "profile": "gfx1100", "nodes": ["cblevins-5930k", "cblevins-7900xtx"], "source": "deploy/gpuprofiles/gfx1100.yaml" } ],
  "modalities": [ { "modality": "embeddings", "level": "supported", "lanes": 7, "evidenced_lanes": 2, "gpu_classes": ["gfx906"], "latest_evidence_date": "2026-10-24", "evidence": ["bge-large-radeonvii", "gte-qwen2-1p5b-radeonvii"] } ],
  "entries": [ { "id": "gfx1100/llamacpp/draft-mtp", "kind": "certificate", "support_level": "certified", "backend": "llamacpp", "gpu_class": "gfx1100", "capability": "draft-mtp", "image": "registry...@sha256:...", "evidenced": true, "evidence": ".loom/60-validation-matrix.md#gfx1100-llamacpp-b11847-2026-10-24", "date": "2026-10-24", "source": "deploy/gpuprofiles/gfx1100.yaml" } ]
}
```

Every entry carries `id`, `kind`, `support_level`, `backend`, `gpu_class`,
`evidenced`, `evidence` (a path, `path#anchor`, MR or ref; empty when there is
none), `date` (`YYYY-MM-DD`, or `null` when no source dates the evidence) and
`source` (the input file). Optional: `nodes`, `capability`, `model`,
`modality`, `canary`, `decision`, `title`, `image`, `evidence_note` (free-text
promotion evidence), `validation_rows` (every row that names a lane).

Output is deterministic: entries sorted by kind, then GPU class, backend and
id (validation rows newest first), no timestamps.

### Vocabularies

- `kind`: `backend`, `certificate`, `lane`, `validation`.
- `support_level`: `certified`, `supported`, `experimental`, `deprecated`,
  `unsupported`, `parked`, `incomplete` (partial certificate), `unverified`,
  `n/a`.
- `decision` (validation rows, and the row behind a backend or lane):
  `promote`, `pass`, `conditional`, `pending`, `fail`, `block`, `skip`,
  `superseded`, `parked`, `other`.
- `modality`: `chat`, `embeddings`, `rerank`, `asr`, `tts`, `audio`, `image`,
  `video`, `vision`, `rag`. The advertised set (`chat`, `embeddings`,
  `rerank`, `asr`, `tts`, `image`, `video`, `rag`) always renders with an
  explicit `level`, so a consumer never infers a claim from a missing key.

## Derivation rules

- **backend**: the GPUProfile's declared `support` (`full` → `supported`).
  Evidence is the *evidence row* (below) with the same GPU class and backend;
  with none, the profile file, `date: null`, `evidenced: false`.
- **certificate**: a `flexinfer.ai/backend-certificate-<backend>-<capability>`
  key that holds a digest-pinned image. `certified` only when the image,
  `-since` and `-evidence` keys are all present (the same rule
  `GetBackendCapabilityCertificate` callers apply); a triple missing one is
  `incomplete`. `-artifact`, `-max-model-len`, `-gpu-util` are attributes, not
  certificates. The backend is the longest backend name the profile declares
  (`vllm-omni-x` → `vllm-omni`).
- **lane**: GPU class from the `kubernetes.io/hostname` node selector via
  `runtime.profiles`, else the only GPUProfile of the lane's GPU vendor; lanes
  with no GPU are class `cpu`. Modality from the backend, `config.embedding` /
  `config.reranking`, the vision capability and name/source/alias markers
  (`pkg/certify/lanes.go:laneModality`). A validation row *names* a lane when
  the lane name appears as a whole word in its `artifact` or `canary_command`
  cell. Support level and decision come from the evidence row among those;
  the evidence pointer is that row or the promotion-evidence annotation,
  whichever is newer (free text that says `matrix row <id>` points at that
  row). Only an annotation → `supported`. Only plan rows (pending, skip) or
  nothing → `unverified`.
- **validation**: one row of the table whose header has the `artifact`,
  `gpu_class`, `backend`, `support_level`, `canary_command` and
  `promotion_decision` columns. Support level after the last `→` of the
  `support_level` cell (`production` → `supported`, `conditional` →
  `experimental`); decision from the first code span or bold text of
  `promotion_decision`; id from the artifact cell's leading code span (`@date`
  appended when the id has none, `#n` for repeats); date from the id, else the
  first date in the row. A row with a different cell count than the header is
  an error (it mis-renders on GitLab too: escape `|` as `\|`).
- **evidence row**: among the matching rows, the newest with a verdict
  (`promote`, `pass`, `conditional`, `fail`, `block`, `parked`), else the
  newest. A pending kill-test does not displace the verdict it may replace.
- **modality level**: over evidenced, non-canary GPU lanes only. `supported`
  when one is supported with `promote`/`pass`/`conditional` (or only an
  annotation); `experimental` when one exists that did not fail or block;
  else `unverified`. `rag` has no lane (it is the proxy's `/v1/rag` read
  path): it needs a validation row whose artifact cell names `rag` or
  `/v1/rag`.

## What it does not say

- Live state: a lane in git may be parked, Idle or unscheduled. A `--cluster`
  mode reading live `Model`/`GPUProfile` objects is not implemented.
- Throughput and energy numbers. Those live in the validation rows' text and,
  for energy, in `flexinfer_lane_energy_joules_total` (metrics spec).
- Evidence outside the Promotion Matrix table (the raw evidence archive
  sections, `.loom/` iteration notes) unless a row, certificate or annotation
  points at it.
