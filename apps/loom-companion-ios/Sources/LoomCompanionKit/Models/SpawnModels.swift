import Foundation

/// Supported headless agent types.
public enum AgentType: String, Codable, Sendable, CaseIterable, Identifiable {
    case claudeCode = "claude-code"
    case codex
    case gemini

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .claudeCode: return "Claude Code"
        case .codex: return "Codex"
        case .gemini: return "Gemini"
        }
    }
}

/// Request body for POST /api/mobile/v1/agent/spawn.
public struct MobileSpawnRequest: Codable, Sendable {
    public let agentType: String
    public let project: String
    public let branch: String?
    public let baseBranch: String?
    public let taskDescription: String
    public let namespace: String?
    public let memoryMB: Int?
    public let cpus: Double?
    public let timeoutMinutes: Int?
    public let multiTurn: Bool?
    public let maxCostUSD: Double?
    public let maxTurns: Int?

    public init(
        agentType: AgentType = .claudeCode,
        project: String,
        branch: String? = nil,
        baseBranch: String? = nil,
        taskDescription: String,
        namespace: String? = nil,
        memoryMB: Int? = nil,
        cpus: Double? = nil,
        timeoutMinutes: Int? = nil,
        multiTurn: Bool? = nil,
        maxCostUSD: Double? = nil,
        maxTurns: Int? = nil
    ) {
        self.agentType = agentType.rawValue
        self.project = project
        self.branch = branch
        self.baseBranch = baseBranch
        self.taskDescription = taskDescription
        self.namespace = namespace
        self.memoryMB = memoryMB
        self.cpus = cpus
        self.timeoutMinutes = timeoutMinutes
        self.multiTurn = multiTurn
        self.maxCostUSD = maxCostUSD
        self.maxTurns = maxTurns
    }

    enum CodingKeys: String, CodingKey {
        case agentType = "agent_type"
        case project
        case branch
        case baseBranch = "base_branch"
        case taskDescription = "task_description"
        case namespace
        case memoryMB = "memory_mb"
        case cpus
        case timeoutMinutes = "timeout_minutes"
        case multiTurn = "multi_turn"
        case maxCostUSD = "max_cost_usd"
        case maxTurns = "max_turns"
    }
}

/// Response from POST /api/mobile/v1/agent/spawn.
public struct MobileSpawnResponse: Codable, Sendable {
    public let spawnId: String
    public let agentId: String
    public let status: String

    enum CodingKeys: String, CodingKey {
        case spawnId = "spawn_id"
        case agentId = "agent_id"
        case status
    }
}

/// Spawn status from GET /api/mobile/v1/agent/spawn/{id}.
public struct MobileSpawnStatus: Codable, Sendable, Identifiable {
    public let spawnId: String
    public let agentId: String
    public let podName: String?
    public let status: String
    public let request: MobileSpawnRequest
    public let startedAt: String
    public let endedAt: String?
    public let error: String?

    public var id: String { spawnId }

    public var isActive: Bool {
        status == "creating" || status == "running"
    }

    enum CodingKeys: String, CodingKey {
        case spawnId = "spawn_id"
        case agentId = "agent_id"
        case podName = "pod_name"
        case status
        case request
        case startedAt = "started_at"
        case endedAt = "ended_at"
        case error
    }
}

/// Response from GET /api/mobile/v1/agent/spawns.
public struct MobileSpawnListResponse: Codable, Sendable {
    public let spawns: [MobileSpawnStatus]
}

// MARK: - Spawn Config (picker data)

/// Response from GET /api/mobile/v1/agent/spawn/config.
public struct SpawnConfig: Codable, Sendable {
    public let agentTypes: [SpawnAgentTypeInfo]
    public let projects: [SpawnProjectInfo]
    public let defaults: SpawnDefaults

    enum CodingKeys: String, CodingKey {
        case agentTypes = "agent_types"
        case projects, defaults
    }
}

/// Agent type with availability flag.
public struct SpawnAgentTypeInfo: Codable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let available: Bool
}

/// Project available for spawning.
public struct SpawnProjectInfo: Codable, Sendable, Identifiable {
    public let name: String
    public let path: String
    public var id: String { name }
}

/// Default spawn configuration values.
public struct SpawnDefaults: Codable, Sendable {
    public let agentType: String
    public let baseBranch: String
    public let memoryMB: Int
    public let cpus: Double
    public let timeoutMinutes: Int

    enum CodingKeys: String, CodingKey {
        case agentType = "agent_type"
        case baseBranch = "base_branch"
        case memoryMB = "memory_mb"
        case cpus
        case timeoutMinutes = "timeout_minutes"
    }
}
