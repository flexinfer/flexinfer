import Foundation

/// Parsed deep link routes for the Loom Companion app.
/// URL scheme: loom://
enum DeepLink: Equatable {
    case dashboard
    case session(id: String)
    case sessions(status: String?)
    case workflow(id: String, approve: Bool)
    case tasks(status: String?)
    case alerts
    case connection

    /// Parse a URL into a DeepLink.
    static func from(_ url: URL) -> DeepLink? {
        guard url.scheme == "loom" else { return nil }

        let host = url.host() ?? ""
        let pathComponents = url.pathComponents.filter { $0 != "/" }
        let queryItems = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []

        switch host {
        case "dashboard":
            return .dashboard

        case "session":
            if let id = pathComponents.first {
                return .session(id: id)
            }
            return nil

        case "sessions":
            let status = queryItems.first(where: { $0.name == "status" })?.value
            return .sessions(status: status)

        case "workflow":
            if let id = pathComponents.first {
                let approve = pathComponents.contains("approve")
                return .workflow(id: id, approve: approve)
            }
            return nil

        case "tasks":
            let status = queryItems.first(where: { $0.name == "status" })?.value
            return .tasks(status: status)

        case "alerts":
            return .alerts

        case "connection":
            return .connection

        default:
            return nil
        }
    }
}
