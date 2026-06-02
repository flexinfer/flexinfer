# Brainstorm — FlexInfer Voice Stack on gfx1100 / gfx906

**Date**: 2026-06-01
**Slug**: voice-stack
**Decision under consideration**: Get the voice stack working in flexinfer and ready to use via a 7900xtx (gfx1100) or radeonvii (gfx906) node.
**Resolved scope (user, 2026-06-01)**: Meeting pipeline (ASR + diarization) as v1, with planning for the full conversational loop (STT→LLM→TTS) next. Placement split across both nodes.

---

## Grounding (current state, verified by exploration 2026-06-01)

| Component | State | Evidence |
|-----------|-------|----------|
| Whisper ASR on gfx1100 (vLLM) | ✅ WORKING (kill-test PASS) | `.loom/ralph-whisper-kill-test-v6-evidence-2026-05-21.md`; `/v1/audio/transcriptions` auto-exposed |
| Production Whisper Model CR | ❌ NOT DEPLOYED (only kill-test CR) | `deploy/models/whisper-kill-test-v3.yaml` |
| Proxy audio routing | ✅ WORKING (catch-all + multipart) | `internal/proxy/proxy.go:403`, `internal/proxy/resolver.go:308-352` |
| Pyannote diarization (gfx906) | ❌ PLANNED, no code | `.loom/asr-diarization-7900xtx-plan-2026-05-18.md` |
| TTS (`/v1/audio/speech`) | ❌ ABSENT (no Kokoro/Piper/XTTS) | no codebase hits |
| Multimodal audio *input* (Qwen-Omni, Gemma4) | 🔄 half-built smoke tests | `deploy/models/qwen25-omni-3b.yaml`, `deploy/models/gemma4-e4b-multimodal.yaml` |

Hardware constraints:
- **gfx1100 (7900xtx: cblevins-7900xtx, cblevins-5930k)**: 24 GiB; runs vLLM-Whisper; FlashAttention via Triton. Shares `7900xtx-textgen` GPU group with the 26B textgen model (contention).
- **gfx906 (radeonvii)**: 16 GiB; vLLM broken (broken-HIP-op ceiling, maintenance-mode arch). Only PyTorch-only models run, via `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` + `HSA_OVERRIDE_GFX_VERSION=9.0.6`.

---

## Phase 1 — Diverge

**A — Ship the ASR we already proved.** Promote kill-tested Whisper to a production Model CR on gfx1100, document, wire one client.
- Bet: First value is batch transcription; work is deploy+docs, not code. Days.
- Risk: If "voice stack" means duplex, ASR-only under-delivers.

**B — Full conversational loop (STT→LLM→TTS).** Add a TTS backend, orchestrate Whisper→LLM→TTS.
- Bet: A voice stack means you talk and it talks back; transcriber is ~30% of it.
- Risk: TTS on ROCm unproven here; new backend type = heaviest path.

**C — Meeting-intelligence pipeline (ASR + diarization).** Whisper (gfx1100) + pyannote (gfx906) behind the proxy. "Who said what, when."
- Bet: Real driver is ICC meeting transcription; diarization is the differentiator.
- Risk: pyannote on gfx906 community fork is fragile.

**E — Node-placement architecture-first.** Keep scarce gfx1100 free; push always-on/cheap parts to underused radeonvii.
- Bet: Binding constraint is GPU contention, not model availability.
- Risk: gfx906 ROCm broken for vLLM; limited to PyTorch-only paths.

**F — Real-time streaming voice.** Partial transcripts, barge-in, WebSocket both directions.
- Bet: Non-streaming won't feel like a usable voice product.
- Risk: vLLM Whisper is batch-oriented; streaming on ROCm is research-grade. Largest scope.

**G — Adopt external orchestration; flexinfer just serves models.** Point Pipecat/LiveKit-agents at flexinfer's `/v1` endpoints.
- Bet: Turn-taking/VAD/barge-in are solved by mature frameworks — reuse beats rebuild.
- Risk: Frameworks assume CUDA/specific endpoints; integration friction.

**H — Define "done" as one canonical demo.** Pick one interaction, make exactly that work end-to-end on one node.
- Bet: Blocker is scope, not tech; one provable interaction kills paralysis.
- Risk: A demo isn't reusable infra.

---

## Phase 2 — Cross-Pollinate

- **A + H → Proven-slice demo**: wrap the already-passing Whisper in one canonical demo (voice memo → transcript+summary via the agent-loop client / row-195). Only path where every component already exists and is kill-tested; work is assembly, not invention.
- **Tension B ↔ A (the real axis)**: A says voice = transcription; B says voice = conversation. C/F/G are flavors of B's bet. Which side you're on = 3-day deploy vs multi-week build.
- **Tension E ↔ everything**: data favors *not* leading with E — gfx906's broken vLLM means placement is constrained by capability (PyTorch-only on radeonvii), so placement is a consequence, not the lead decision.

---

## Phase 3 — Converge

**Recommended: Framing C (ASR + diarization) on a split placement, built as a sequenced thin slice.**

The split is both chosen and hardware-forced: gfx1100 is the only node that runs vLLM-Whisper; gfx906 can only host PyTorch-only models (pyannote) via the community fork.

- **gfx1100**: production Whisper Model CR (promote kill-tested CR — Framing A, ~days, proven).
- **gfx906**: pyannote diarization sibling Deployment via `mixa3607/pytorch-gfx906`.
- **proxy**: single base URL — `/v1/audio/transcriptions` (auto) + static `/diarize` route.
- **demo gate**: meeting audio → diarized transcript defines "ready to use."

**Sequence** (step 2 is the only unproven part):
1. Deploy Whisper (proven).
2. **Kill-test pyannote-on-gfx906 (risky — see below).**
3. Proxy `/diarize` route.
4. Canonical demo: meeting audio → diarized transcript.

If step 2 fails, step 1 still ships an ASR-only product at zero extra cost.

**Runner-up: Framing A (ASR-only).** Tips it if the pyannote/gfx906 fork is too fragile to operate (pinned ROCm 6.3.3, gfx906 maintenance-mode, prior broken-HIP-op ceilings on this node). Not a separate plan — it's the graceful degradation of the recommended path.

**Open question (gates the planned conversational/TTS step B):** Where does TTS run? gfx906 PyTorch-only (Piper/Kokoro) frees the big GPU but inherits fork fragility; gfx1100 is safer but contends with the 26B group. Needs its own kill-test before B is committed.

---

## Riskiest assumption + kill-test

**Load-bearing assumption**: `pyannote/speaker-diarization-3.1` runs end-to-end (produces correct RTTM speaker turns) on `cblevins-radeonvii` (gfx906/Vega20) under `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` with `HSA_OVERRIDE_GFX_VERSION=9.0.6`, co-resident with FLUX Fill.

**Kill test (≤30 min)**: Build `Dockerfile.pyannote-rocm-gfx906`, run a one-shot pod on radeonvii, POST a 30-second 2-speaker WAV to `/diarize`, assert ≥2 distinct speaker segments with plausible timestamps — and confirm GPU residency stays ~500 MiB without OOMing FLUX Fill's NF4 footprint.

**Failure mode if wrong**: Split architecture collapses to ASR-only (runner-up). ~70% of work (Whisper deploy, proxy, demo harness) is reusable; only the diarization service is lost.

**Status**: not run.

---

## Handoff

- Direction chosen → hand to `plan-loom-core` to turn this into a sliced spec, OR `feature-dev` to start with step 1 (Whisper production Model CR).
- The existing master plan `.loom/asr-diarization-7900xtx-plan-2026-05-18.md` already has slices 1–6; reconcile this brainstorm's sequencing into it rather than duplicating.
- Riskiest-assumption kill-test (pyannote/gfx906) is slice 1's completion criterion — slice 2+ blocked until it runs live.
