`flexinfer-bench diarize_conformance` (D5-a): scores a `diarize=true` job's speaker turns and the
standalone pyannote Deployment's against a reference RTTM (md-eval DER with a collar, overlapped
speech and optimal speaker mapping, in `pkg/transcript` with an RTTM reader), checks the job's
words carry speakers and takes the window's gte p95, and prints the KT-DIAR matrix row. With
`scripts/kt-diar.py` (gte/bge probe, the job, the standalone baseline) and the owner window plan
`.loom/32-iteration-plan-d5a-diarize-job-2026-11-01.md`; matrix row `gfx906-diarize-job-2026-11-xx`
is pending.
