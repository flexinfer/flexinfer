The first request drained from a cold-start queue no longer 502s while kube-proxy programs
the lane's Service. The controller marks a Model Ready from the same pod readiness that adds
the pod to the EndpointSlice, so the drain could dial the ClusterIP before kube-proxy had
programmed it. The dial was refused on both attempts 50 ms apart: a cold Wan 2.1 request got
`lane_unresponsive` and a request 150 ms later was served (KT-FLEX, 2026-11-03). Drained
requests now retry a refused dial for up to 5 s, with the backoff doubling from 50 ms to 1 s.
Requests to a Ready lane keep the single 50 ms retry.
