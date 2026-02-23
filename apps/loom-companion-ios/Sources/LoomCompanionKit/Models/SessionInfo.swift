import Foundation

/// Session status values.
public enum SessionStatus: String, Decodable, Sendable {
    case active
    case ended
}

/// Matches `SessionInfo` from bridge/agent.go.
public struct SessionInfo: Decodable, Identifiable, Sendable {
    public let id: String
    public let agentId: String
    public let namespace: String
    public let status: SessionStatus
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

/// Response wrapper for session list endpoint.
public struct SessionsResponse: Decodable, Sendable {
    public let sessions: [SessionInfo]

    public init(sessions: [SessionInfo]) {
        self.sessions = sessions
    }
}

/// Response wrapper for single session endpoint.
public struct SessionDetailResponse: Decodable, Sendable {
    public let session: SessionInfo

    public init(session: SessionInfo) {
        self.session = session
    }
}
