CI job `publish_serving_rocm_gfx1100_v033` publishes the `gfx1100-serving-v033` persona (vLLM
0.33.0-rocm72, base pinned by digest) as `runtime:rocm-gfx1100-serving-v033-<sha>`, with the same inputs
and change rules as the 0.32 persona. The 0.33 images were built by hand until now; the workhorse
promotion to 0.33 needs a reproducible build that carries text plugin 0.12.0 and plugin-common 0.5.0.
