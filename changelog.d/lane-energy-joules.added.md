Where GPU power sampling is enabled, the node agent integrates it into
`flexinfer_lane_energy_joules_total{model,node,gpu}`: each step's energy,
`(P_prev + P_now) / 2 × Δt`, is split equally among the Models Ready on the node's GPU, and
an idle card's energy is attributed to nobody. The chart's Mission Control dashboard gains a
"Power & Efficiency" row with GPU board power and proxy-reported tokens per joule per lane.
The agent's namespaced Role gains `get`/`list` on `models.ai.flexinfer` only while
`agent.gpuPower.nodes` is non-empty; with the default empty list only the dashboard ConfigMap
changes.
