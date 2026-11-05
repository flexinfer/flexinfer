# Matrix

## Validation Contract

| Field | Required value |
|---|---|
| `artifact` | not the promotion table |

## Promotion Matrix

| `artifact` | `gpu_class` | `backend` | `support_level` | `canary_command` | `promotion_decision` |
|---|---|---|---|---|---|
| `kt-embed-2026-10-31` — kill-test plan naming `embed-lane` | `gfx1100/b` | `llamacpp` | `supported` | TBD | `pending` |
| `gfx1100-vllm-2026-10-27` — warm lane on `chat-lane-long` | `gfx1100/xtx` (canary) → `gfx1100/b` | `vllm` 0.32 | `experimental` → `supported` | `curl -d '{"model":"embed-lane"}'` | **`promote`**: serves |
| `gfx1100-llamacpp-2026-10-24` — llama.cpp `a\|b` repin | `gfx1100/xtx + b` | `llamacpp` b11847 | `supported` (repinned) | none | **`pass` on the riskiest assumption** |
| `gfx1100-diffusers-2026-10-01` — `video-canary` | `gfx1100/xtx` | `diffusers` | `experimental` | none | `fail` |
| **Required canary: `gfx1100` textgen** — chat-lane-longer only | `gfx1100/b` | `vllm` | `production` | none | `conditional` |
| Proxy routing fix (2026-05-14) | `n/a` (proxy) | `n/a` | `n/a` | none | `pass` |
| Proxy routing fix (2026-05-14) | `n/a` (proxy) | `n/a` | `n/a` | none | `superseded` |

## Raw Evidence Archive

| `artifact` | `gpu_class` | `backend` | `support_level` | `canary_command` | `promotion_decision` |
|---|---|---|---|---|---|
| `ignored-second-table` | `gfx906/x` | `vllm` | `supported` | none | `promote` |
