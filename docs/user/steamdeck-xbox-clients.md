# Steam Deck as the Game Hub (FlexInfer + Xbox One X)

The Steam Deck is the user-facing hub for both game sources. FlexInfer owns
exactly one of them (the Sunshine/Moonlight path); the Xbox path is pure
client-side configuration documented here so it lives somewhere findable.
No Xbox protocol, account, or lifecycle handling belongs in FlexInfer.

| Source | Path | FlexInfer's role |
|---|---|---|
| FlexInfer 7900 XTX node | Moonlight → Sunshine | Lease, drain/restore, runtime, health (`flexinfer game …`) |
| Xbox One X | Xbox browser Remote Play (Edge) | None — documented client recipe only |
| Xbox One X | [Greenlight](https://flathub.org/en/apps/io.github.unknownskl.greenlight) | None — third-party fallback |
| FlexInfer node on the TV | [Moonlight UWP for Xbox](https://github.com/TheElixZammuto/moonlight-xbox) | Optional, experimental |

Keep both as Steam library shortcuts (a "Games" collection works well):
one Moonlight entry, one Xbox Remote Play entry.

## Shortcut 1 — FlexInfer games (Moonlight)

1. Install Moonlight from Discover (Flatpak) in Desktop Mode and add it to
   Steam ("Add a Non-Steam Game").
2. Start a lease: `flexinfer game preflight cblevins-7900xtx`, then
   `flexinfer game start cblevins-7900xtx --for 2h --wait`.
3. Pair/connect Moonlight to the node (host `192.168.50.125`; pairing
   persists in `/home/flexinfer-gaming` on the node across sessions).
4. `flexinfer game stop cblevins-7900xtx` (or the TTL/idle expiry) returns
   the GPU to inference automatically.

Streaming settings that behave well here: HEVC, 8-bit (the card has no
10-bit HW encode entrypoint), ~50–60 Mbps. The full server-side runbook is
`docs/user/gaming-mode-runbook.md`.

## Shortcut 2 — Xbox One X Remote Play (browser path)

Xbox officially supports Remote Play from an Xbox One through a
[supported browser](https://www.xbox.com/en-US/consoles/remote-play);
Microsoft documents the Deck-specific Edge controller setup for Cloud
Gaming, and the same recipe is reused here with the Remote Play URL — that
combination needs an on-device test before trusting it for the couch.

1. **On the Xbox One X**: Settings → Devices & connections → Remote
   features → enable; power mode **Sleep/Instant-On**; run the built-in
   remote play test. Prefer Ethernet.
2. **On the Deck (Desktop Mode)**: install **Microsoft Edge** from Discover.
3. Grant Edge controller/device access with Microsoft's documented Flatpak
   override ([Steam Deck Edge setup](https://support.microsoft.com/en-us/edge/xbox-cloud-gaming-in-microsoft-edge-with-steam-deck)):

   ```bash
   flatpak --user override --filesystem=/run/udev:ro com.microsoft.Edge
   ```

4. Add Edge to Steam and set launch options to open Remote Play in kiosk
   mode (append after the existing `@@u @@` placeholder):

   ```
   --window-size=1024,640 --force-device-scale-factor=1.25 --device-scale-factor=1.25 --kiosk "https://www.xbox.com/remoteplay"
   ```

5. Back in Gaming Mode: sign in once, then test video, audio, controls,
   suspend/resume, and reconnect.
6. **Fallback**: if the browser path fails controller or codec-wise, install
   [Greenlight](https://flathub.org/en/apps/io.github.unknownskl.greenlight)
   (Flathub) — a maintained third-party Xbox Remote Play/xCloud client with
   Steam Deck support. Treat it as a fallback, not a dependency.

Do not plan around XBPlay for new installs — its Steam listing says it is no
longer available for purchase.

## Optional — Xbox as a living-room FlexInfer client

The community [Moonlight UWP client](https://github.com/TheElixZammuto/moonlight-xbox)
(sideloaded in Dev Mode) can point the Xbox at the FlexInfer Sunshine host so
the TV gets the stream without another device. It is early-stage software:
fine as an experiment, inappropriate as the reason the gaming lease exists.
