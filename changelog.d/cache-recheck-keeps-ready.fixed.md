A cache Job that re-runs for an artifact already verified for the current spec no longer takes the
cache out of ready. The pvc:// check, stage and copy Jobs and the SharedPVC HF prefetch Job have
no TTL, but the `ops/cluster-cleanup` janitor deletes completed Jobs after 6 h and the controller
then recreates them. While the new Job ran, `status.cache.ready` was false. Idle lanes went
Idle→Pending→Idle (11–16 transitions per lane per week on `qwen35-35b-clean-mtp-32k`,
`qwen35-9b-ablit-rp`, `openjev-0p8b-crossencoder-radeonvii` and
`qwen35-35b-clean-gptq-workhorse-128k`). Warm lanes were scaled to zero and cold-started, as
`gte-qwen2-1p5b-cpu-5930k` was about 11 times a week. The cache now stays ready, with the re-run
shown in `jobPhase` and a `re-verifying …` message, when the previous pass recorded it verified by
the same Job at the current generation. A failed re-run still revokes readiness. So does a Job
replaced because the source, cache PVC or source-ready Job changed, and so does any spec edit since
the verification.
