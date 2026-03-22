import Foundation

/// Session status values.
public enum SessionStatus: String, Decodable, Sendable {
    case active
    case ended
    case summarized
    case unknown

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = (try? container.decode(String.self))?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = SessionStatus(rawValue: raw) ?? .unknown
    }

    public var isTerminal: Bool {
        switch self {
        case .ended, .summarized:
            return true
        case .active, .unknown:
            return false
        }
    }
}

/// Matches `SessionInfo` from bridge/agent.go.
public struct SessionInfo: Decodable, Identifiable, Sendable {
    public let id: String
    public let agentId: String
    public let namespace: String
    public var status: SessionStatus
    public let description: String
    public let startedAt: String
    public let endedAt: String?
    public let entryCount: Int
    public let totalTokens: Int

    enum CodingKeys: String, CodingKey {
        case id
        case agentId = "agent_id"
        case namespace
        case status
        case description
        case startedAt = "started_at"
        case endedAt = "ended_at"
        case entryCount = "entry_count"
        case totalTokens = "total_tokens"
    }

    public init(id: String, agentId: String, namespace: String, status: SessionStatus, description: String, startedAt: String, endedAt: String? = nil, entryCount: Int, totalTokens: Int) {
        self.id = id
        self.agentId = agentId
        self.namespace = namespace
        self.status = status
        self.description = description
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.entryCount = entryCount
        self.totalTokens = totalTokens
    }
}

/// Summary of a namespace with session/agent counts.
public struct NamespaceSummary: Decodable, Identifiable, Sendable {
    public let namespace: String
    public let sessionCount: Int
    public let agentCount: Int
    public let activeAgents: Int

    public var id: String { namespace }

    enum CodingKeys: String, CodingKey {
        case namespace
        case sessionCount = "session_count"
        case agentCount = "agent_count"
        case activeAgents = "active_agents"
    }
}

/// Response wrapper for the namespaces endpoint.
public struct NamespacesResponse: Decodable, Sendable {
    public let namespaces: [NamespaceSummary]
}

/// Response wrapper for session list endpoint.
public struct SessionsResponse: Decodable, Sendable {
    public let sessions: [SessionInfo]

    public init(sessions: [SessionInfo]) {
        self.sessions = sessions
    }
}

/// Response wrapper for single session endpoint with rich detail data.
public struct SessionDetailResponse: Decodable, Sendable {
    public let session: SessionInfo
    public let entryBreakdown: [EntryTypeBucket]?
    public let topEntries: [SessionTopEntry]?
    public let decisions: [SessionTopEntry]?
    public let errors: [SessionTopEntry]?
    public let topFiles: [TouchedFile]?
    public let tasks: SessionTaskSummary?

    enum CodingKeys: String, CodingKey {
        case session
        case entryBreakdown = "entry_breakdown"
        case topEntries = "top_entries"
        case decisions
        case errors
        case topFiles = "top_files"
        case tasks
    }

    public init(session: SessionInfo) {
        self.session = session
        self.entryBreakdown = nil
        self.topEntries = nil
        self.decisions = nil
        self.errors = nil
        self.topFiles = nil
        self.tasks = nil
    }
}

/// Response from POST /api/mobile/v1/sessions (create session).
public struct SessionCreateResponse: Decodable, Sendable {
    public let sessionId: String
    public let recalledContext: String?
    public let alreadyExisted: Bool

    enum CodingKeys: String, CodingKey {
        case sessionId = "session_id"
        case recalledContext = "recalled_context"
        case alreadyExisted = "already_existed"
    }

    public init(sessionId: String, recalledContext: String? = nil, alreadyExisted: Bool = false) {
        self.sessionId = sessionId
        self.recalledContext = recalledContext
        self.alreadyExisted = alreadyExisted
    }
}

/// Response from POST /api/mobile/v1/sessions/{id}/end.
public struct SessionEndResponse: Decodable, Sendable {
    public let ended: Bool
    public let sessionId: String

    enum CodingKeys: String, CodingKey {
        case ended
        case sessionId = "session_id"
    }

    public init(ended: Bool, sessionId: String) {
        self.ended = ended
        self.sessionId = sessionId
    }
}
