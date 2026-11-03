`flexinfer certify render` generates `docs/certification-matrix.json`, the public
certification matrix: per GPU class (gfx1100 RX 7900 XTX, gfx906 Radeon VII, sm_52 GTX 980 Ti)
the declared backend posture, the backend-capability certificates, one entry per lane manifest
and one per Promotion Matrix row of `.loom/60-validation-matrix.md`, each with a
`support_level`, backend, GPU class, evidence pointer and date, plus a per-modality claim
(chat, embeddings, rerank, ASR, TTS, image, video, RAG). It renders offline from repository files;
`make certify-matrix` regenerates the checked-in copy and the `certify_matrix` CI job fails a
merge request whose inputs changed without it (warn-only on master). Schema and rules:
`docs/specs/certification-matrix.md`.
