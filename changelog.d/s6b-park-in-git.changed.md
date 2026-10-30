The 5930k runtime DaemonSet's park is now in git. A new per-profile chart value, `parked: true`,
adds `flexinfer.ai/quantization-pause: "false"` to the runtime DaemonSet's nodeSelector. No node
carries that label, so the DaemonSet stays at desired=0. The `gfx1100-5930k` profile in
`values-k3s.yaml` sets it, and the rendered selector now matches the live one, which until now
existed only through a 2026-04-04 kubectl patch. Un-parking in S6b is now one line in the values
file. Four 5930k lanes now set `config.dedicatedDeployment: true` so that un-parking cannot move
them onto the runtime pod's vLLM 0.17 image: `qwen38-27b-autoround-workhorse`,
`qwen36-27b-autoround-workhorse`, `qwen36-27b-fable-gptq` and `stable-audio-open-gfx1100`. The
flag only changes how a lane is routed. A new controller test renders each of these lanes with
and without it and gets the same Deployment, so nothing changes in the cluster when this merges.
