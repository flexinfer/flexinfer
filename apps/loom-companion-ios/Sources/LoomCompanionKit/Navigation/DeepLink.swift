import Foundation

/// Parsed deep link routes for the Loom Companion app.
///
/// **URL scheme**: `loom://`
///
/// The deep-link surface is *first-class*: every meaningful object in the app
/// has a shareable `loom://` URL. Filtered lists encode their filters in the
/// query string so a shared link reproduces the exact view the sender was
/// looking at.
///
/// See `docs/deep-links.md` for the canonical URL catalogue.
public enum DeepLink: Equatable, Sendable {
    // Primary surfaces
    case dashboard
    case people
    case work
    case alerts
    case connection

    // People · sessions
    case session(id: String)
    case sessions(status: String?, agentId: String?)

    // People · agents
    case agent(id: String)
    case agents(status: String?, type: String?)

    // Work · tasks
    case tasks(status: String?, agentId: String?, sessionId: String?)

    // Work · workflows
    case workflow(id: String, approve: Bool)

    // Work · spawn (remote execution)
    case spawn(id: String)

    // Work · handoff inbox
    case handoff

    // Alerts · single alert
    case alert(id: String)

    // One-shot configuration — typically issued by `make mobile-app-run-device`
    // over USB via `xcrun devicectl device open-url` so a freshly installed
    // build lands already connected to the cluster HUD. The bearer token and
    // Cloudflare Access client secret ride along in the URL, so only ever use
    // this over a trusted transport (USB is fine; do not paste into Messages).
    case configure(ConfigureSpec)

    /// Destination tab group for routing.
    public enum DestinationGroup: Sendable {
        case dashboard
        case people
        case work
        case alerts
        case connection
    }

    /// Credentials + mode payload for a `loom://configure` deep link.
    /// Equatable so tests can round-trip the case cleanly.
    public struct ConfigureSpec: Equatable, Sendable {
        public let mode: String            // "gateway" | "lan"
        public let url: String             // e.g. "https://hud.flexinfer.ai"
        public let bearer: String
        public let cfClientID: String?
        public let cfClientSecret: String?

        public init(
            mode: String,
            url: String,
            bearer: String,
            cfClientID: String? = nil,
            cfClientSecret: String? = nil
        ) {
            self.mode = mode
            self.url = url
            self.bearer = bearer
            self.cfClientID = cfClientID
            self.cfClientSecret = cfClientSecret
        }
    }

    // MARK: - Parsing

    /// Parse a URL into a DeepLink. Returns nil for unknown hosts or malformed paths.
    public static func from(_ url: URL) -> DeepLink? {
        guard url.scheme == "loom" else { return nil }

        let host = url.host()?.lowercased() ?? ""
        let pathComponents = url.pathComponents.filter { $0 != "/" }
        let queryItems = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []

        func query(_ name: String) -> String? {
            queryItems.first(where: { $0.name == name })?.value?.nilIfEmpty
        }

        switch host {
        // Primary surfaces
        case "dashboard": return .dashboard
        case "people": return .people
        case "work": return .work
        case "alerts": return .alerts
        case "connection": return .connection
        case "handoff", "handoffs": return .handoff

        // Single-object detail routes
        case "session":
            guard let id = pathComponents.first?.nilIfEmpty else { return nil }
            return .session(id: id)

        case "agent":
            guard let id = pathComponents.first?.nilIfEmpty else { return nil }
            return .agent(id: id)

        case "workflow":
            guard let id = pathComponents.first?.nilIfEmpty else { return nil }
            let approve = pathComponents.contains("approve")
            return .workflow(id: id, approve: approve)

        case "spawn":
            guard let id = pathComponents.first?.nilIfEmpty else { return nil }
            return .spawn(id: id)

        case "alert":
            guard let id = pathComponents.first?.nilIfEmpty else { return nil }
            return .alert(id: id)

        case "configure":
            guard let urlValue = query("url"),
                  let bearer = query("bearer")
            else {
                return nil
            }
            let mode = query("mode")?.lowercased() ?? "gateway"
            return .configure(ConfigureSpec(
                mode: mode,
                url: urlValue,
                bearer: bearer,
                cfClientID: query("cf_id"),
                cfClientSecret: query("cf_secret")
            ))

        // Filtered list routes
        case "sessions":
            return .sessions(status: query("status"), agentId: query("agent"))

        case "agents":
            return .agents(status: query("status"), type: query("type"))

        case "tasks":
            return .tasks(status: query("status"), agentId: query("agent"), sessionId: query("session"))

        default:
            return nil
        }
    }

    // MARK: - Building (inverse of parse)

    /// Canonical string form of this deep link. `DeepLink.from(url(.dashboard)!) == .dashboard` always holds.
    public var urlString: String {
        switch self {
        case .dashboard: return "loom://dashboard"
        case .people: return "loom://people"
        case .work: return "loom://work"
        case .alerts: return "loom://alerts"
        case .connection: return "loom://connection"
        case .handoff: return "loom://handoff"

        case .session(let id): return "loom://session/\(Self.escape(id))"
        case .agent(let id): return "loom://agent/\(Self.escape(id))"
        case .spawn(let id): return "loom://spawn/\(Self.escape(id))"
        case .alert(let id): return "loom://alert/\(Self.escape(id))"

        case .workflow(let id, let approve):
            let path = approve ? "\(Self.escape(id))/approve" : Self.escape(id)
            return "loom://workflow/\(path)"

        case .sessions(let status, let agentId):
            return Self.withQuery("loom://sessions", [("status", status), ("agent", agentId)])

        case .agents(let status, let type):
            return Self.withQuery("loom://agents", [("status", status), ("type", type)])

        case .tasks(let status, let agentId, let sessionId):
            return Self.withQuery(
                "loom://tasks",
                [("status", status), ("agent", agentId), ("session", sessionId)]
            )

        case .configure(let spec):
            return Self.withQuery(
                "loom://configure",
                [
                    ("mode", spec.mode),
                    ("url", spec.url),
                    ("bearer", spec.bearer),
                    ("cf_id", spec.cfClientID),
                    ("cf_secret", spec.cfClientSecret),
                ]
            )
        }
    }

    /// URL representation for use with `ShareLink` and `openURL`.
    public var url: URL? {
        URL(string: urlString)
    }

    private static func escape(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? value
    }

    private static func withQuery(_ base: String, _ pairs: [(String, String?)]) -> String {
        let present = pairs.compactMap { key, value -> URLQueryItem? in
            guard let value = value?.nilIfEmpty else { return nil }
            return URLQueryItem(name: key, value: value)
        }
        guard !present.isEmpty, var comps = URLComponents(string: base) else { return base }
        comps.queryItems = present
        return comps.string ?? base
    }

    // MARK: - Routing

    public var destinationGroup: DestinationGroup {
        switch self {
        case .dashboard: return .dashboard
        case .people, .session, .sessions, .agent, .agents: return .people
        case .work, .workflow, .tasks, .spawn, .handoff: return .work
        case .alerts, .alert: return .alerts
        case .connection, .configure: return .connection
        }
    }

    // MARK: - Display helpers (for share-link UI)

    /// Short human label for share sheets ("Session svc-abc", "Agent claude-code", etc).
    public var shareTitle: String {
        switch self {
        case .dashboard: return "Loom Dashboard"
        case .people: return "Loom · Agents"
        case .work: return "Loom · Work"
        case .alerts: return "Loom · Alerts"
        case .connection: return "Loom · Connection"
        case .handoff: return "Loom · Handoffs"
        case .session(let id): return "Session \(id)"
        case .sessions(let status, _):
            return status.map { "Sessions (\($0))" } ?? "Sessions"
        case .agent(let id): return "Agent \(id)"
        case .agents(let status, _):
            return status.map { "Agents (\($0))" } ?? "Agents"
        case .tasks(let status, _, _):
            return status.map { "Tasks (\($0))" } ?? "Tasks"
        case .workflow(let id, let approve):
            return approve ? "Approve workflow \(id)" : "Workflow \(id)"
        case .spawn(let id): return "Spawn \(id)"
        case .alert(let id): return "Alert \(id)"
        case .configure: return "Configure Loom Companion"
        }
    }
}

// MARK: - String convenience

private extension String {
    var nilIfEmpty: String? {
        let trimmed = trimmingCharacters(in: .whitespaces)
        return trimmed.isEmpty ? nil : trimmed
    }
}
