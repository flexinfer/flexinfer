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
    public let parentSessionId: String?
    public let rootSessionId: String?

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
        case parentSessionId = "parent_session_id"
        case rootSessionId = "root_session_id"
    }

    public init(
        id: String,
        agentId: String,
        namespace: String,
        status: SessionStatus,
        description: String,
        startedAt: String,
        endedAt: String? = nil,
        entryCount: Int,
        totalTokens: Int,
        parentSessionId: String? = nil,
        rootSessionId: String? = nil
    ) {
        self.id = id
        self.agentId = agentId
        self.namespace = namespace
        self.status = status
        self.description = description
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.entryCount = entryCount
        self.totalTokens = totalTokens
        self.parentSessionId = parentSessionId
        self.rootSessionId = rootSessionId
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try c.decode(String.self, forKey: .id)
        self.agentId = try c.decodeIfPresent(String.self, forKey: .agentId) ?? ""
        self.namespace = try c.decodeIfPresent(String.self, forKey: .namespace) ?? ""
        self.status = try c.decodeIfPresent(SessionStatus.self, forKey: .status) ?? .unknown
        self.description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        self.startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt) ?? ""
        self.endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
        self.entryCount = try c.decodeIfPresent(Int.self, forKey: .entryCount) ?? 0
        self.totalTokens = try c.decodeIfPresent(Int.self, forKey: .totalTokens) ?? 0
        self.parentSessionId = try c.decodeIfPresent(String.self, forKey: .parentSessionId)
        self.rootSessionId = try c.decodeIfPresent(String.self, forKey: .rootSessionId)
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

public struct SessionTreeNode: Decodable, Identifiable, Sendable {
    public let session: SessionInfo
    public let depth: Int
    public let childCount: Int
    public let activeChildCount: Int
    public let children: [SessionTreeNode]

    public var id: String { session.id }

    enum CodingKeys: String, CodingKey {
        case session
        case depth
        case childCount = "child_count"
        case activeChildCount = "active_child_count"
        case children
    }
}

public struct SessionTreeSummary: Decodable, Sendable {
    public let rootCount: Int
    public let activeSessions: Int
    public let orphanSessions: Int
    public let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case rootCount = "root_count"
        case activeSessions = "active_sessions"
        case orphanSessions = "orphan_sessions"
        case updatedAt = "updated_at"
    }
}

public struct SessionTreeResponse: Decodable, Sendable {
    public let roots: [SessionTreeNode]
    public let orphans: [SessionTreeNode]
    public let summary: SessionTreeSummary
}

public struct SessionActivityTask: Decodable, Identifiable, Sendable {
    public let id: String
    public let title: String
    public let status: String
    public let priority: String
    public let tags: [String]
    public let workflowId: String?
    public let pipelineId: Int?
    public let createdAt: String
    public let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id, title, status, priority, tags
        case workflowId = "workflow_id"
        case pipelineId = "pipeline_id"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        self.title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        self.status = try c.decodeIfPresent(String.self, forKey: .status) ?? "unknown"
        self.priority = try c.decodeIfPresent(String.self, forKey: .priority) ?? "medium"
        self.tags = try c.decodeIfPresent([String].self, forKey: .tags) ?? []
        self.workflowId = try c.decodeIfPresent(String.self, forKey: .workflowId)
        self.pipelineId = try c.decodeIfPresent(Int.self, forKey: .pipelineId)
        self.createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        self.updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
    }
}

public struct SessionActivityPipeline: Decodable, Identifiable, Sendable {
    public let id: Int
    public let project: String
    public let ref: String
    public let status: String
    public let currentStage: String?
    public let failedJobCount: Int
    public let webURL: String?

    enum CodingKeys: String, CodingKey {
        case id, project, ref, status
        case currentStage = "current_stage"
        case failedJobCount = "failed_job_count"
        case webURL = "web_url"
    }
}

public struct SessionActivityResponse: Decodable, Sendable {
    public let sessionId: String
    public let tasks: [SessionActivityTask]
    public let pipelines: [SessionActivityPipeline]
    public let taskCount: Int
    public let pipelineCount: Int

    public var hasFailedPipeline: Bool {
        pipelines.contains { $0.status.lowercased() == "failed" || $0.failedJobCount > 0 }
    }

    enum CodingKeys: String, CodingKey {
        case sessionId = "session_id"
        case tasks
        case pipelines
        case taskCount = "task_count"
        case pipelineCount = "pipeline_count"
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
