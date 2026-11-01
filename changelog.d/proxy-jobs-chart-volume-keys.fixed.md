The proxy Deployment template rendered duplicate `volumeMounts` and `volumes` keys once
`proxy.jobs.enabled` was set (the QoS ConfigMap mount and the job spool were added by parallel
MRs); the job spool and the pvc store now join the QoS mount under one key each.
