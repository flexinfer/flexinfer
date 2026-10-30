The Model controller's worker count can now be configured with
`--model-max-concurrent-reconciles`, set through the chart value
`controller.modelMaxConcurrentReconciles`. Both default to 1, and at 1 the chart renders no flag,
so the controller pod template does not change. Different Models are safe to reconcile in
parallel: they share no unguarded state. Members of one shared-GPU group still elect one at a time
under a per-group lock, because each member's pass rewrites the active-services annotation on every
member's Service. Raise the value only on a controller image that has the flag, because older images
exit on an unknown flag.
