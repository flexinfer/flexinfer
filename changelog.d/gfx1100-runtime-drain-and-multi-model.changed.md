Both gfx1100 runtime profiles in `deploy/system/values-k3s.yaml` now set
`FLEXINFER_RUNTIME_REQUEST_DRAIN=1`, and `gfx1100-5930k` also sets
`FLEXINFER_RUNTIME_MULTI_MODEL=1`, matching `gfx906`. In-flight requests now finish before a lane
swap or a spec-change reload releases the card, which the S6b move of the 27B workhorse onto the
runtime path needs. The XTX change rolls `flexinfer-runtime-gfx1100` once. It had no models loaded
on 2026-10-27, and its runtime-managed voice lanes (snac) reload on next demand. The
`flexinfer-runtime-gfx1100-5930k` DaemonSet is desired=0, so nothing rolls there until S6b
un-parks it.
