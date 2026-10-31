The manager deletes finished InferenceJobs once their `ttlAfterFinished` has passed (the proxy's
storage finalizer removes their outputs first) and exports `flexinfer_jobs{namespace,phase}` and
`flexinfer_jobs_gc_deleted_total`. The proxy exports `flexinfer_jobs_submitted_total`,
`flexinfer_jobs_finished_total`, `flexinfer_jobs_running`, `flexinfer_jobs_queue_wait_seconds`,
`flexinfer_jobs_run_seconds`, `flexinfer_jobs_retries_total` and
`flexinfer_jobs_webhook_deliveries_total`. New CLI verb: `flexinfer jobs list|get|cancel|delete|content`.
