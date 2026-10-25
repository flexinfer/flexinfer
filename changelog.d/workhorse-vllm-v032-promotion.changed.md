The 5930k production text lane `qwen38-27b-autoround-workhorse` runs vLLM 0.32.0
(`runtime@sha256:cd108c9e…`, the native-W4A16 image on the upstream v0.32.0 ROCm wheel, !1155),
promoted from 0.26 after the XTX warm-lane canary. It boots on Model Runner V2 with a
140,753-token KV pool (2.15x at 65k context, up from 128,837), answers the canary probes
identically at 41.5 tok/s, and passed a 10-minute concurrency-2 soak in production with 0 errors
and a clean host. `calculateKvScales` left the manifest because vLLM 0.28 removed the flag.
Rollback is reverting !1155; the 0.26 digest `ad93ae72` stays resident and held on the node.
