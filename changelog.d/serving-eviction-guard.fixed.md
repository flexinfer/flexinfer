Serving pods rank behind CI job pods for kubelet disk-pressure eviction. The three runtime
DaemonSet profiles (gfx1100, gfx1100-5930k, gfx906) and the two Mills text lanes
(`qwen38-27b-autoround-workhorse`, `qwen38-27b-xtx-warm-canary`) now carry a 2Gi
`ephemeral-storage` request; the runtime profiles also set `priorityClassName:
flexinfer-serving-critical` (new optional per-profile chart key). Kubelet evicts pods that
exceed their ephemeral request first, and a pod with no request exceeds it at the first
byte — on 2026-09-13 that evicted the workhorse lane (then image GC dropped its 11 GB runtime
image: 10 min re-pull, ~20 min outage) ahead of the CI job pods that had filled the disk.
