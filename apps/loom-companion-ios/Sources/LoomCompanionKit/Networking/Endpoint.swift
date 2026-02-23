import Foundation

/// All mobile v1 API routes.
public enum Endpoint: Sendable {
    case ping
    case dashboard
    case sessions
    case sessionDetail(id: String)
    case sessionEvents(id: String, limit: Int? = nil)
    case createSession(agentId: String, namespace: String? = nil, description: String? = nil, autoRecall: Bool? = nil)
    case endSession(id: String, summarize: Bool? = nil)
    case eventsStream
    case audit(source: String? = nil, limit: Int? = nil)

    var method: String {
        switch self {
        case .ping, .dashboard, .sessions, .sessionDetail, .sessionEvents, .eventsStream, .audit:
            return "GET"
        case .createSession, .endSession:
            return "POST"
        }
    }

    var path: String {
        switch self {
        case .ping:
            return "/api/mobile/v1/ping"
        case .dashboard:
            return "/api/mobile/v1/dashboard"
        case .sessions:
            return "/api/mobile/v1/sessions"
        case let .sessionDetail(id):
            return "/api/mobile/v1/sessions/\(id)"
        case let .sessionEvents(id, _):
            return "/api/mobile/v1/sessions/\(id)/events"
        case .createSession:
            return "/api/mobile/v1/sessions"
        case let .endSession(id, _):
            return "/api/mobile/v1/sessions/\(id)/end"
        case .eventsStream:
            return "/api/mobile/v1/events/stream"
        case .audit:
            return "/api/mobile/v1/audit"
        }
    }

    var isMutation: Bool {
        method == "POST"
    }

    func urlRequest(baseURL: URL) throws -> URLRequest {
        guard var components = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false) else {
            throw LoomAPIError.invalidURL(url: baseURL.absoluteString + path)
        }

        // Query parameters
        switch self {
        case let .sessionEvents(_, limit):
            if let limit {
                components.queryItems = [URLQueryItem(name: "limit", value: String(limit))]
            }
        case let .audit(source, limit):
            var items: [URLQueryItem] = []
            if let source { items.append(URLQueryItem(name: "source", value: source)) }
            if let limit { items.append(URLQueryItem(name: "limit", value: String(limit))) }
            if !items.isEmpty { components.queryItems = items }
        default:
            break
        }

        guard let url = components.url else {
            throw LoomAPIError.invalidURL(url: components.string ?? path)
        }

        var request = URLRequest(url: url)
        request.httpMethod = method

        // Request body
        switch self {
        case let .createSession(agentId, namespace, description, autoRecall):
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            var body: [String: Any] = ["agent_id": agentId]
            if let namespace { body["namespace"] = namespace }
            if let description { body["description"] = description }
            if let autoRecall { body["auto_recall"] = autoRecall }
            request.httpBody = try JSONSerialization.data(withJSONObject: body)

        case let .endSession(_, summarize):
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            var body: [String: Any] = [:]
            if let summarize { body["summarize"] = summarize }
            request.httpBody = try JSONSerialization.data(withJSONObject: body)

        default:
            break
        }

        return request
    }
}
