The proxy no longer holds a request for minutes on a shared-GPU lane that a higher-priority lane
holds the card against. A request without `Prefer: wait` to such a lane waits at most the preempted
wait (`FLEXINFER_PROXY_PREEMPTED_WAIT`, default 20 s; annotation `flexinfer.ai/preempted-wait` per
lane) and then gets 503 `model_preempted` with `Retry-After`; it still stamps demand. Before, it
waited up to the lane's `coldStartTimeout` (25 min on the XTX warm lane) while clients gave up at
120 s (KT-JOBS bar 2, 2026-11-17 and 2026-11-18). A queued request whose client disconnects now
gives its cold-start queue slot back at once, so 100 abandoned requests no longer leave the lane
answering `queue_full` with nobody waiting. A request the proxy refuses with a typed 503 (for
example a `Prefer: wait=0` probe) no longer counts as interactive use, so it does not keep the job
runner from releasing the lane (`kept_interactive` on the Wan lane in the 2026-11-18 run).
