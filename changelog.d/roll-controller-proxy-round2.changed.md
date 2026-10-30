Rolled flexinfer-controller and flexinfer-proxy to master 3cb6cde0 (tag 20261030-153940): one
runtime load payload for the controller and the proxy, command-wrapper lanes kept off the runtime
path, verified caches that stay ready while their check Job re-runs, gfx906 HWS queue admission in
the shared-group election, and the opt-in image-scoped compile cache. Model reconcile concurrency
stays at 1.
