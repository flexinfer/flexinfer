Cold-started requests now run under the lane's `flexinfer.ai/lane-first-byte-budget`. The
cold-start queue drained without loading it, so a lane whose first request after a proxy
restart was a cold start used the 120 s default: a cold Wan 2.1 clip (budget 600 s) was cut
at 120 s while the card was still rendering (KT-FLEX, 2026-11-03).
