import Foundation

/// All mobile v1 API routes.
public enum Endpoint: Sendable {
    case ping
    case dashboard
    case sessions
    case sessionDetail(id: String)
    case sessionEvents(id: String, limit: Int? = nil)
    case tasks(status: MobileTaskStatus? = nil, agentId: String? = nil, sessionId: String? = nil, limit: Int? = nil, search: String? = nil)
    case workflows(status: MobileWorkflowStatus? = nil, agentId: String? = nil, limit: Int? = nil)
    case workflowDetail(id: String)
    case presence(status: MobilePresenceStatus? = nil, agentId: String? = nil, limit: Int? = nil)
    case memoryStats
    case memoryItems(tier: MobileMemoryTier = .working, query: String? = nil, limit: Int? = nil)
    case stream(types: [String]? = nil, agentId: String? = nil, sessionId: String? = nil, limit: Int? = nil)
    case topology
    case graphStats
    case graphEntities(type: String? = nil, query: String? = nil, limit: Int? = nil)
    case graphPath(sourceId: String, targetId: String, maxDepth: Int? = nil)
    case reasoningChains(status: MobileReasoningStatus? = nil, limit: Int? = nil)
    case reasoningChainDetail(id: String)
    case createSession(agentId: String, namespace: String? = nil, description: String? = nil, autoRecall: Bool? = nil)
    case endSession(id: String, summarize: Bool? = nil)
    case eventsStream
    case audit(source: String? = nil, limit: Int? = nil)

    var method: String {
        switch self {
        case .ping, .dashboard, .sessions, .sessionDetail, .sessionEvents,
             .tasks, .workflows, .workflowDetail, .presence, .memoryStats,
             .memoryItems, .stream, .topology, .graphStats, .graphEntities,
             .graphPath, .reasoningChains, .reasoningChainDetail,
             .eventsStream, .audit:
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
        case .tasks:
            return "/api/mobile/v1/tasks"
        case .workflows:
            return "/api/mobile/v1/workflows"
        case let .workflowDetail(id):
            return "/api/mobile/v1/workflows/\(id)"
        case .presence:
            return "/api/mobile/v1/presence"
        case .memoryStats:
            return "/api/mobile/v1/memory/stats"
        case .memoryItems:
            return "/api/mobile/v1/memory/items"
        case .stream:
            return "/api/mobile/v1/stream"
        case .topology:
            return "/api/mobile/v1/topology"
        case .graphStats:
            return "/api/mobile/v1/graph/stats"
        case .graphEntities:
            return "/api/mobile/v1/graph/entities"
        case .graphPath:
            return "/api/mobile/v1/graph/path"
        case .reasoningChains:
            return "/api/mobile/v1/reasoning/chains"
        case let .reasoningChainDetail(id):
            return "/api/mobile/v1/reasoning/chains/\(id)"
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
        case let .tasks(status, agentId, sessionId, limit, search):
            var items: [URLQueryItem] = []
            if let status {
                items.append(URLQueryItem(name: "status", value: status.rawValue))
            }
            if let agentId {
                items.append(URLQueryItem(name: "agent_id", value: agentId))
            }
            if let sessionId {
                items.append(URLQueryItem(name: "session_id", value: sessionId))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            if let search {
                items.append(URLQueryItem(name: "search", value: search))
            }
            if !items.isEmpty { components.queryItems = items }
        case let .workflows(status, agentId, limit):
            var items: [URLQueryItem] = []
            if let status {
                items.append(URLQueryItem(name: "status", value: status.rawValue))
            }
            if let agentId {
                items.append(URLQueryItem(name: "agent_id", value: agentId))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            if !items.isEmpty { components.queryItems = items }
        case let .presence(status, agentId, limit):
            var items: [URLQueryItem] = []
            if let status {
                items.append(URLQueryItem(name: "status", value: status.rawValue))
            }
            if let agentId {
                items.append(URLQueryItem(name: "agent_id", value: agentId))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            if !items.isEmpty { components.queryItems = items }
        case let .memoryItems(tier, query, limit):
            var items: [URLQueryItem] = [URLQueryItem(name: "tier", value: tier.rawValue)]
            if let query {
                items.append(URLQueryItem(name: "query", value: query))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            components.queryItems = items
        case let .stream(types, agentId, sessionId, limit):
            var items: [URLQueryItem] = []
            if let types, !types.isEmpty {
                items.append(URLQueryItem(name: "types", value: types.joined(separator: ",")))
            }
            if let agentId {
                items.append(URLQueryItem(name: "agent_id", value: agentId))
            }
            if let sessionId {
                items.append(URLQueryItem(name: "session_id", value: sessionId))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            if !items.isEmpty { components.queryItems = items }
        case let .graphEntities(type, query, limit):
            var items: [URLQueryItem] = []
            if let type {
                items.append(URLQueryItem(name: "type", value: type))
            }
            if let query {
                items.append(URLQueryItem(name: "q", value: query))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            if !items.isEmpty { components.queryItems = items }
        case let .graphPath(sourceId, targetId, maxDepth):
            var items: [URLQueryItem] = [
                URLQueryItem(name: "source_id", value: sourceId),
                URLQueryItem(name: "target_id", value: targetId),
            ]
            if let maxDepth {
                items.append(URLQueryItem(name: "max_depth", value: String(maxDepth)))
            }
            components.queryItems = items
        case let .reasoningChains(status, limit):
            var items: [URLQueryItem] = []
            if let status {
                items.append(URLQueryItem(name: "status", value: status.rawValue))
            }
            if let limit {
                items.append(URLQueryItem(name: "limit", value: String(limit)))
            }
            if !items.isEmpty { components.queryItems = items }
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
