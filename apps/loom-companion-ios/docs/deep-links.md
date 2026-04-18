# Deep Link URL Scheme

Canonical reference for the `loom://` URL scheme used by the Loom Companion iOS app. Every meaningful object and list surface in the app has a shareable URL so an operator can send a co-worker straight to the exact view they were looking at.

- **Scheme**: `loom://`
- **Parser**: [`DeepLink`](../Sources/LoomCompanionKit/Navigation/DeepLink.swift) in `LoomCompanionKit`
- **Round-trip guarantee**: `DeepLink.from(URL(string: link.urlString)!) == link` holds for every valid `DeepLink` case (covered by [`DeepLinkTests`](../Tests/LoomCompanionKitTests/Navigation/DeepLinkTests.swift))
- **Case-insensitive hosts**: `loom://Dashboard` and `loom://dashboard` are equivalent
- **Empty query values ignored**: `loom://tasks?status=` is treated as `loom://tasks`

## Primary surfaces

| URL | Destination |
|---|---|
| `loom://dashboard` | Dashboard |
| `loom://people` | People tab |
| `loom://work` | Work tab |
| `loom://alerts` | Alerts tab |
| `loom://connection` | Connection diagnostics |
| `loom://handoff` | Work tab → handoff inbox (alias: `loom://handoffs`) |

## Single-object detail routes

| URL | Opens |
|---|---|
| `loom://session/{id}` | Session detail view |
| `loom://agent/{id}` | Agent detail view (preset filter on People → Agents) |
| `loom://workflow/{id}` | Workflow detail view (read-only) |
| `loom://workflow/{id}/approve` | Workflow detail with automatic approval of the pending step |
| `loom://spawn/{id}` | Spawn (remote execution) detail view |
| `loom://alert/{id}` | Alerts tab with the specified alert highlighted |

IDs must be URL-path-safe; the builder percent-encodes automatically when shared from the app.

## Filtered list routes

List surfaces accept query-string filters so shared links reproduce the exact filter state.

| URL | Opens |
|---|---|
| `loom://sessions` | Sessions list (no filter) |
| `loom://sessions?status=active` | Sessions list, status filter preset |
| `loom://sessions?status=active&agent=claude-code` | Sessions list filtered by status + agent |
| `loom://agents` | Agents list (no filter) |
| `loom://agents?status=idle&type=gemini` | Agents list filtered by presence + agent-type |
| `loom://tasks?status=blocked` | Work → tasks, blocked-only |
| `loom://tasks?status=blocked&agent=claude-code` | Tasks filtered by status + agent |
| `loom://tasks?session={id}` | Tasks scoped to a session |

**Query-key conventions** (stable across routes):

- `status` — the list's status filter (`active`/`idle`/`offline` for presence; `pending`/`blocked`/`in_progress` for tasks; etc.)
- `agent` — scope to an agent by ID
- `session` — scope to a session by ID
- `type` — filter by agent type (`claude-code`, `gemini`, `codex`, `copilot`, `kilocode`, `antigravity`)

The builder emits query items in this stable order: `status`, `agent`, `session`, `type`. Shared links are therefore canonical.

## Sharing from the app

Detail-view toolbars expose a `Share` menu with two actions:

- **Copy link** — writes the canonical `loom://` URL to the pasteboard (triggers light haptic)
- **Share** — opens the system share sheet with the URL + a human-readable title (e.g. `"Session svc-abc123"`)

List rows (sessions, agents, alerts) expose the same pair via long-press context menu. The `LoomShareLink` and `LoomCopyLinkButton` components in [`Components/LoomShareLink.swift`](../Sources/LoomCompanion/Components/LoomShareLink.swift) wrap these affordances so every detail surface uses the same visual language.

## Testing deep links locally

Launch the app in the simulator first (so `ai.flexinfer.loom.companion` is registered), then:

```bash
# Boot + launch
xcrun simctl boot "iPhone 17 Pro"
xcrun simctl launch booted ai.flexinfer.loom.companion

# Navigate via deep link
xcrun simctl openurl booted "loom://session/svc-abc123"
xcrun simctl openurl booted "loom://tasks?status=blocked&agent=claude-code"
xcrun simctl openurl booted "loom://workflow/wf-1/approve"
```

On a real device, tapping a `loom://` URL in Notes/Messages/Mail launches the app and routes to the target.

## Adding a new route

1. Add a case to the `DeepLink` enum in [`Sources/LoomCompanionKit/Navigation/DeepLink.swift`](../Sources/LoomCompanionKit/Navigation/DeepLink.swift).
2. Extend `DeepLink.from(_:)` with the new host → case mapping.
3. Extend `urlString` with the inverse build.
4. Extend `destinationGroup` so `ContentView.handleDeepLink` picks the right tab.
5. Handle the case in `ContentView.handleDeepLink(_:)` — typically this means setting a pending-ID on `NavigationCoordinator` and switching `selectedTab`.
6. Add a round-trip test to [`DeepLinkTests.swift`](../Tests/LoomCompanionKitTests/Navigation/DeepLinkTests.swift).
7. Add a row to this document.
