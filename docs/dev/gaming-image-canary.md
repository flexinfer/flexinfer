# Gaming Image Canary (Sunshine security + exact-hardware gate)

Every gaming session activation is gated on the runtime image being listed in
`gaming.approvedImages` (`deploy/system/values-k3s.yaml` → the
`flexinfer-gaming-policy` ConfigMap). The list ships **empty** and only a
digest that passed this canary may be added. There is deliberately no
in-session override: a `GamingSession` cannot approve its own image.

## Why the gate exists

- The last known-streaming pin, Sunshine `2025.924.154138`
  (`build/Dockerfile.runtime`), is affected by
  [GHSA-ph75-mgxh-mv57 / CVE-2026-32253](https://github.com/LizardByte/Sunshine/security/advisories/GHSA-ph75-mgxh-mv57),
  a **critical remote authentication bypass** fixed in `v2026.516.143833`.
- The patched `2026.516` release was already tried here and **reverted the
  same day**: its wlroots capture path (broken from `v2026.407`, Sunshine
  [#5018](https://github.com/LizardByte/Sunshine/issues/5018)) segfaults on
  stream start on the exact `cblevins-7900xtx` headless sway + AMD VAAPI
  stack (`f88f8678b` → `de205c44a`). Upstream merged
  [PR #5030](https://github.com/LizardByte/Sunshine/pull/5030) for
  wlroots/multi-GPU/AMD DMA-BUF problems, but a release note is not proof on
  this host.

So: the old image is insecure, the patched image is (last we checked)
unusable. Candidates are one of:

1. a **newer upstream release** carrying both the auth fix and the #5030
   capture fixes;
2. a **changed capture/encoding path** (e.g. different capture backend) on a
   secure release; or
3. the known-working `2025.924` build with the **authentication fix
   backported** (last resort; maintenance burden).

## Building a candidate

Off-CI on the GPU builder, like every heavy runtime build:

```bash
SUNSHINE_VERSION=<candidate-version> ./build/build-runtime.sh gfx1100-gaming --push
```

`SUNSHINE_VERSION` overrides the Dockerfile's safe-window pin for this build
only (leave the Dockerfile pin + its window comment intact until a candidate
passes). Use `docker --context 7900xtx`. Record the pushed digest.

Fail-fast sanity check before the full build: install the candidate `.deb` in
a bare `ubuntu:24.04` container to catch packaging breaks early.

## The 30-minute kill test (exact hardware, isolated)

Run against an **isolated** runtime container on `cblevins-7900xtx` (not the
live DaemonSet), LAN-restricted:

1. Start the candidate image privileged with `/dev/dri`, `/dev/uinput`,
   `/dev/input`, `/run/udev`, host network, on the node.
2. Confirm the Sunshine version reports the security fix, and that capture
   binds headless sway on `renderD128` (NAVI31, **not** the iGPU) with
   h264/hevc/av1 VAAPI encoders probed.
3. Pair and connect the Steam Deck via Moonlight at 1280x800/60.
4. Play 15 minutes of real motion with controller input and audio; disconnect
   and reconnect once.
5. PASS requires: no Sunshine segfault (check node `dmesg`, not container
   logs — the crash logs nothing), no backend restart
   (`flexinfer_runtime_gaming_backend_restarts_total` flat), no black frames,
   no capture-device switch, no sustained decoder failures on the Deck.
6. Security check: an unpaired client must not be able to establish an
   authenticated control session against the web UI/API.

Prior art: `.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md` (original
substrate proof), `docs/user/gaming-mode-runbook.md` (diagnosis table).

## On pass

1. Add the digest to `gaming.approvedImages` in
   `deploy/system/values-k3s.yaml` with a dated comment naming the Sunshine
   version and this canary. Merge; Flux updates the policy ConfigMap.
2. Update the `SUNSHINE_VERSION` pin + window comment in
   `build/Dockerfile.runtime` to the passing version.
3. Restore the gaming stanza on the `gfx1100` runtime profile (image digest,
   `hostNetwork: true`, `gaming: true`, input/udev/gaming-data mounts,
   `GAMING_*` env) — shape in commit `0de40f610`. From then on the node is
   statically gaming-capable and sessions are purely
   `flexinfer game start/stop`.

## On fail

Keep `approvedImages` empty. Sessions stay refused with
`CapabilityReady=False/ImageNotApproved` — the lease/CLI UX must not ship on
top of a vulnerable or crashing streaming runtime. File the failure evidence
next to this doc and try the next candidate class.
