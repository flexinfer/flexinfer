`scripts/kt-qos-priority.py` is the KT-QOS harness for caller QoS classes: two saturating background
streams plus an interactive request every 10 s against a lane, directly (fcfs or vLLM priority) or
through flexinfer-proxy, with interactive TTFT p50/p95, background gaps, engine decode, preemptions,
MTP pos-0 acceptance and greedy parity, and a `--verdict` mode that applies the D3 pass rule. The
canary `deploy/debug/qwen38-27b-qos-canary.yaml` and the window plan
`.loom/32-iteration-plan-kt-qos-2026-10-31.md` are ready for the owner to schedule.
