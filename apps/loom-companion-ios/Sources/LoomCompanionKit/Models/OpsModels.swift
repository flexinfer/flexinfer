import Foundation

public enum MobileTaskStatus: String, Decodable, Sendable {
    case pending
    case inProgress = "in_progress"
    case blocked
    case completed
    case unknown

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = (try? container.decode(String.self))?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = MobileTaskStatus(rawValue: raw) ?? .unknown
    }
}

public enum MobileWorkflowStatus: String, Decodable, Sendable {
    case running
    case waitingApproval = "waiting_approval"
    case completed
    case failed
    case cancelled
    case unknown

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = (try? container.decode(String.self))?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = MobileWorkflowStatus(rawValue: raw) ?? .unknown
    }
}

public enum MobilePresenceStatus: String, Decodable, Sendable {
    case active
    case idle
    case offline
    case unknown

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = (try? container.decode(String.self))?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = MobilePresenceStatus(rawValue: raw) ?? .unknown
    }
}

public enum MobileMemoryTier: String, Decodable, Sendable, CaseIterable {
    case working
    case shortTerm = "short_term"
    case longTerm = "long_term"
    case unknown

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = (try? container.decode(String.self))?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = MobileMemoryTier(rawValue: raw) ?? .unknown
    }
}

public struct MobileTaskCounts: Decodable, Sendable {
    public let pending: Int
    public let inProgress: Int
    public let blocked: Int
    public let completed: Int

    enum CodingKeys: String, CodingKey {
        case pending
        case inProgress = "in_progress"
        case blocked
        case completed
    }

    public init(pending: Int, inProgress: Int, blocked: Int, completed: Int) {
        self.pending = pending
        self.inProgress = inProgress
        self.blocked = blocked
        self.completed = completed
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.pending = try container.decodeIfPresent(Int.self, forKey: .pending) ?? 0
        self.inProgress = try container.decodeIfPresent(Int.self, forKey: .inProgress) ?? 0
        self.blocked = try container.decodeIfPresent(Int.self, forKey: .blocked) ?? 0
        self.completed = try container.decodeIfPresent(Int.self, forKey: .completed) ?? 0
    }
}

public struct MobileTask: Decodable, Identifiable, Sendable {
    public let id: String
    public let sessionId: String
    public let agentId: String
    public let namespace: String
    public let title: String
    public let context: String
    public let priority: String
    public let status: MobileTaskStatus
    public let tags: [String]
    public let blockedBy: [String]
    public let createdAt: String
    public let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case sessionId = "session_id"
        case agentId = "agent_id"
        case namespace
        case title
        case context
        case priority
        case status
        case tags
        case blockedBy = "blocked_by"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    public init(
        id: String,
        sessionId: String,
        agentId: String,
        namespace: String,
        title: String,
        context: String,
        priority: String,
        status: MobileTaskStatus,
        tags: [String],
        blockedBy: [String],
        createdAt: String,
        updatedAt: String
    ) {
        self.id = id
        self.sessionId = sessionId
        self.agentId = agentId
        self.namespace = namespace
        self.title = title
        self.context = context
        self.priority = priority
        self.status = status
        self.tags = tags
        self.blockedBy = blockedBy
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try container.decode(String.self, forKey: .id)
        self.sessionId = try container.decodeIfPresent(String.self, forKey: .sessionId) ?? ""
        self.agentId = try container.decodeIfPresent(String.self, forKey: .agentId) ?? ""
        self.namespace = try container.decodeIfPresent(String.self, forKey: .namespace) ?? ""
        self.title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        self.context = try container.decodeIfPresent(String.self, forKey: .context) ?? ""
        self.priority = try container.decodeIfPresent(String.self, forKey: .priority) ?? "medium"
        self.status = try container.decodeIfPresent(MobileTaskStatus.self, forKey: .status) ?? .unknown
        self.tags = try container.decodeIfPresent([String].self, forKey: .tags) ?? []
        self.blockedBy = try container.decodeIfPresent([String].self, forKey: .blockedBy) ?? []
        self.createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        self.updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
    }
}

public struct MobileTasksResponse: Decodable, Sendable {
    public let tasks: [MobileTask]
    public let counts: MobileTaskCounts
}

public struct MobileWorkflow: Decodable, Identifiable, Sendable {
    public let id: String
    public let name: String?
    public let status: MobileWorkflowStatus
    public let currentStep: String?
    public let progress: Double
    public let startedAt: String
    public let completedAt: String?
    public let error: String?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case status
        case currentStep = "current_step"
        case progress
        case startedAt = "started_at"
        case completedAt = "completed_at"
        case error
    }
}

public struct MobileWorkflowStep: Decodable, Identifiable, Sendable {
    public let id: String
    public let name: String
    public let status: MobileWorkflowStatus
    public let type: String?
    public let error: String?
}

public struct MobileWorkflowEvent: Decodable, Identifiable, Sendable {
    public let id: String
    public let eventType: String
    public let timestamp: String
    public let stepId: String?
    public let stepName: String?
    public let details: String?

    enum CodingKeys: String, CodingKey {
        case id
        case eventType = "event_type"
        case timestamp
        case stepId = "step_id"
        case stepName = "step_name"
        case details
    }
}

public struct MobileWorkflowDetail: Decodable, Sendable {
    public let id: String
    public let name: String?
    public let status: MobileWorkflowStatus
    public let currentStep: String?
    public let progress: Double
    public let startedAt: String
    public let completedAt: String?
    public let error: String?
    public let steps: [MobileWorkflowStep]?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case status
        case currentStep = "current_step"
        case progress
        case startedAt = "started_at"
        case completedAt = "completed_at"
        case error
        case steps
    }
}

public struct MobileWorkflowsResponse: Decodable, Sendable {
    public let workflows: [MobileWorkflow]
    public let pendingApprovals: Int

    enum CodingKeys: String, CodingKey {
        case workflows
        case pendingApprovals = "pending_approvals"
    }
}

public struct MobileWorkflowDetailResponse: Decodable, Sendable {
    public let workflow: MobileWorkflowDetail
    public let events: [MobileWorkflowEvent]
}

public struct MobilePresenceAgent: Decodable, Identifiable, Sendable {
    public let id: String
    public let agentId: String
    public let sessionId: String?
    public let status: MobilePresenceStatus
    public let agentType: String
    public let description: String
    public let currentTask: String
    public let activeFiles: [String]
    public let branch: String
    public let prURL: String?
    public let worktreeId: String
    public let lastHeartbeat: String
    public let registeredAt: String

    enum CodingKeys: String, CodingKey {
        case agentId = "agent_id"
        case sessionId = "session_id"
        case status
        case agentType = "agent_type"
        case description
        case currentTask = "current_task"
        case activeFiles = "active_files"
        case branch
        case prURL = "pr_url"
        case worktreeId = "worktree_id"
        case lastHeartbeat = "last_heartbeat"
        case registeredAt = "registered_at"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.agentId = try container.decode(String.self, forKey: .agentId)
        self.id = self.agentId
        self.sessionId = try container.decodeIfPresent(String.self, forKey: .sessionId)
        self.status = try container.decode(MobilePresenceStatus.self, forKey: .status)
        self.agentType = try container.decode(String.self, forKey: .agentType)
        self.description = try container.decode(String.self, forKey: .description)
        self.currentTask = try container.decodeIfPresent(String.self, forKey: .currentTask) ?? ""
        self.activeFiles = try container.decodeIfPresent([String].self, forKey: .activeFiles) ?? []
        self.branch = try container.decodeIfPresent(String.self, forKey: .branch) ?? ""
        self.prURL = try container.decodeIfPresent(String.self, forKey: .prURL)
        self.worktreeId = try container.decodeIfPresent(String.self, forKey: .worktreeId) ?? ""
        self.lastHeartbeat = try container.decodeIfPresent(String.self, forKey: .lastHeartbeat) ?? ""
        self.registeredAt = try container.decodeIfPresent(String.self, forKey: .registeredAt) ?? ""
    }
}

public struct MobileFileClaim: Decodable, Identifiable, Sendable {
    public let id: String
    public let agentId: String
    public let sessionId: String
    public let filePath: String
    public let claimType: String
    public let reason: String
    public let createdAt: String
    public let expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case agentId = "agent_id"
        case sessionId = "session_id"
        case filePath = "file_path"
        case claimType = "claim_type"
        case reason
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }
}

public struct MobileWorktree: Decodable, Identifiable, Sendable {
    public let id: String
    public let assignmentId: String
    public let agentId: String
    public let sessionId: String
    public let worktreePath: String
    public let branch: String
    public let baseBranch: String
    public let purpose: String
    public let status: String
    public let createdAt: String
    public let releasedAt: String?
    public let gitStatus: String?

    enum CodingKeys: String, CodingKey {
        case assignmentId = "assignment_id"
        case agentId = "agent_id"
        case sessionId = "session_id"
        case worktreePath = "worktree_path"
        case branch
        case baseBranch = "base_branch"
        case purpose
        case status
        case createdAt = "created_at"
        case releasedAt = "released_at"
        case gitStatus = "git_status"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.assignmentId = try container.decode(String.self, forKey: .assignmentId)
        self.id = self.assignmentId
        self.agentId = try container.decode(String.self, forKey: .agentId)
        self.sessionId = try container.decodeIfPresent(String.self, forKey: .sessionId) ?? ""
        self.worktreePath = try container.decodeIfPresent(String.self, forKey: .worktreePath) ?? ""
        self.branch = try container.decodeIfPresent(String.self, forKey: .branch) ?? ""
        self.baseBranch = try container.decodeIfPresent(String.self, forKey: .baseBranch) ?? ""
        self.purpose = try container.decodeIfPresent(String.self, forKey: .purpose) ?? ""
        self.status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        self.createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        self.releasedAt = try container.decodeIfPresent(String.self, forKey: .releasedAt)
        self.gitStatus = try container.decodeIfPresent(String.self, forKey: .gitStatus)
    }
}

public struct MobilePresenceSummary: Decodable, Sendable {
    public let activeAgents: Int
    public let idleAgents: Int
    public let offlineAgents: Int
    public let totalAgents: Int
    public let claimCount: Int
    public let worktreeCount: Int

    enum CodingKeys: String, CodingKey {
        case activeAgents = "active_agents"
        case idleAgents = "idle_agents"
        case offlineAgents = "offline_agents"
        case totalAgents = "total_agents"
        case claimCount = "claim_count"
        case worktreeCount = "worktree_count"
    }
}

public struct MobilePresenceResponse: Decodable, Sendable {
    public let agents: [MobilePresenceAgent]
    public let claims: [MobileFileClaim]
    public let worktrees: [MobileWorktree]
    public let spawns: [MobilePresenceSpawn]
    public let summary: MobilePresenceSummary

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        agents = try container.decode([MobilePresenceAgent].self, forKey: .agents)
        claims = try container.decode([MobileFileClaim].self, forKey: .claims)
        worktrees = try container.decode([MobileWorktree].self, forKey: .worktrees)
        spawns = try container.decodeIfPresent([MobilePresenceSpawn].self, forKey: .spawns) ?? []
        summary = try container.decode(MobilePresenceSummary.self, forKey: .summary)
    }

    enum CodingKeys: String, CodingKey {
        case agents, claims, worktrees, spawns, summary
    }
}

/// Lightweight spawn info returned alongside presence data.
public struct MobilePresenceSpawn: Decodable, Sendable, Identifiable {
    public let spawnId: String
    public let agentId: String
    public let podName: String?
    public let status: String
    public let project: String
    public let branch: String
    public let taskDescription: String
    public let agentType: String
    public let startedAt: String
    public let endedAt: String?
    public let error: String?

    public var id: String { spawnId }

    public var isActive: Bool {
        status == "creating" || status == "building" || status == "running"
    }

    enum CodingKeys: String, CodingKey {
        case spawnId = "spawn_id"
        case agentId = "agent_id"
        case podName = "pod_name"
        case status, project, branch
        case taskDescription = "task_description"
        case agentType = "agent_type"
        case startedAt = "started_at"
        case endedAt = "ended_at"
        case error
    }
}

public struct MobileMemoryTierStats: Decodable, Sendable {
    public let items: Int
    public let tokens: Int
}

public struct MobileMemoryCompression: Decodable, Sendable {
    public let ratio: Double
    public let added24h: Int
    public let compressed24h: Int
    public let estimatedSaved: Int
    public let compressedItems: Int

    enum CodingKeys: String, CodingKey {
        case ratio
        case added24h = "added_24h"
        case compressed24h = "compressed_24h"
        case estimatedSaved = "estimated_saved"
        case compressedItems = "compressed_items"
    }
}

public struct MobileMemoryStats: Decodable, Sendable {
    public let workingMemory: MobileMemoryTierStats
    public let shortTermMemory: MobileMemoryTierStats
    public let longTermMemory: MobileMemoryTierStats
    public let totalItems: Int
    public let totalTokens: Int
    public let compression: MobileMemoryCompression

    enum CodingKeys: String, CodingKey {
        case workingMemory = "working_memory"
        case shortTermMemory = "short_term_memory"
        case longTermMemory = "long_term_memory"
        case totalItems = "total_items"
        case totalTokens = "total_tokens"
        case compression
    }
}

public struct MobileMemoryStatsResponse: Decodable, Sendable {
    public let stats: MobileMemoryStats
}

public struct MobileMemoryItem: Decodable, Identifiable, Sendable {
    public let id: String
    public let title: String
    public let content: String?
    public let tier: MobileMemoryTier
    public let importance: String
    public let importanceScore: Double
    public let tokens: Int
    public let status: String?
    public let category: String?
    public let accessedAt: String?
    public let lastAccessed: String?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case content
        case tier
        case importance
        case importanceScore = "importance_score"
        case tokens
        case status
        case category
        case accessedAt = "accessed_at"
        case lastAccessed = "last_accessed"
    }
}

public struct MobileMemoryItemsResponse: Decodable, Sendable {
    public let items: [MobileMemoryItem]
    public let tier: MobileMemoryTier
}

public struct MobileStreamEntry: Decodable, Identifiable, Sendable {
    public let id: String
    public let entryType: String
    public let agentId: String
    public let agent: String
    public let namespace: String
    public let title: String
    public let content: String?
    public let timestamp: String
    public let score: Double?

    enum CodingKeys: String, CodingKey {
        case id
        case entryType = "entry_type"
        case agentId = "agent_id"
        case agent
        case namespace
        case title
        case content
        case timestamp
        case score
    }
}

public struct MobileStreamResponse: Decodable, Sendable {
    public let entries: [MobileStreamEntry]
}

public struct MobileTopologyNode: Decodable, Identifiable, Sendable {
    public let id: String
    public let agentId: String
    public let status: MobilePresenceStatus
    public let agentType: String
    public let currentTask: String?
    public let branch: String?
    public let prURL: String?
    public let namespace: String?

    enum CodingKeys: String, CodingKey {
        case agentId = "agent_id"
        case status
        case agentType = "agent_type"
        case currentTask = "current_task"
        case branch
        case prURL = "pr_url"
        case namespace
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.agentId = try container.decode(String.self, forKey: .agentId)
        self.id = self.agentId
        self.status = try container.decode(MobilePresenceStatus.self, forKey: .status)
        self.agentType = try container.decodeIfPresent(String.self, forKey: .agentType) ?? ""
        self.currentTask = try container.decodeIfPresent(String.self, forKey: .currentTask)
        self.branch = try container.decodeIfPresent(String.self, forKey: .branch)
        self.prURL = try container.decodeIfPresent(String.self, forKey: .prURL)
        self.namespace = try container.decodeIfPresent(String.self, forKey: .namespace)
    }
}

public struct MobileTopologyEdge: Decodable, Sendable {
    public let source: String
    public let target: String
    public let edgeType: String
    public let weight: Int
    public let label: String?
    public let status: String?

    enum CodingKeys: String, CodingKey {
        case source
        case target
        case edgeType = "edge_type"
        case weight
        case label
        case status
    }
}

public struct MobileTopologyCluster: Decodable, Sendable {
    public let project: String
    public let agentIDs: [String]

    enum CodingKeys: String, CodingKey {
        case project
        case agentIDs = "agent_ids"
    }
}

public struct MobileTopologyResponse: Decodable, Sendable {
    public let nodes: [MobileTopologyNode]
    public let edges: [MobileTopologyEdge]
    public let clusters: [MobileTopologyCluster]
    public let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case nodes
        case edges
        case clusters
        case updatedAt = "updated_at"
    }
}

public struct MobileGraphStats: Decodable, Sendable {
    public let totalEntities: Int
    public let totalRelations: Int
    public let entityTypes: [String: Int]
    public let relationTypes: [String: Int]

    enum CodingKeys: String, CodingKey {
        case totalEntities = "total_entities"
        case totalRelations = "total_relations"
        case entityTypes = "entity_types"
        case relationTypes = "relation_types"
    }
}

public struct MobileGraphStatsResponse: Decodable, Sendable {
    public let stats: MobileGraphStats
}

public struct MobileGraphEntity: Decodable, Identifiable, Sendable {
    public let id: String
    public let name: String
    public let entityType: String
    public let description: String?
    public let namespace: String?
    public let properties: [String: AnyCodable]

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case entityType = "entity_type"
        case description
        case namespace
        case properties
    }
}

public struct MobileGraphEntitiesResponse: Decodable, Sendable {
    public let entities: [MobileGraphEntity]
}

public struct MobileGraphPath: Decodable, Sendable {
    public let nodes: [MobileGraphEntity]
    public let length: Int
}

public struct MobileGraphPathResponse: Decodable, Sendable {
    public let path: MobileGraphPath
}

public enum MobileReasoningStatus: String, Decodable, Sendable {
    case active
    case completed
    case abandoned
    case unknown

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = (try? container.decode(String.self))?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = MobileReasoningStatus(rawValue: raw) ?? .unknown
    }
}

public struct MobileReasoningStep: Decodable, Identifiable, Sendable {
    public let id: String
    public let description: String
    public let confidence: Double
    public let evidence: String?
    public let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case description
        case confidence
        case evidence
        case createdAt = "created_at"
    }
}

public struct MobileReasoningChain: Decodable, Identifiable, Sendable {
    public let id: String
    public let title: String
    public let status: MobileReasoningStatus
    public let stepCount: Int
    public let confidence: Double?
    public let createdAt: String
    public let completedAt: String?
    public let steps: [MobileReasoningStep]?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case status
        case stepCount = "step_count"
        case confidence
        case createdAt = "created_at"
        case completedAt = "completed_at"
        case steps
    }
}

public struct MobileReasoningChainsResponse: Decodable, Sendable {
    public let chains: [MobileReasoningChain]
}

public struct MobileReasoningChainDetailResponse: Decodable, Sendable {
    public let chain: MobileReasoningChain
}

public struct MobileControlPlaneCostTopAgent: Decodable, Sendable {
    public let agentId: String
    public let callCount: Int
    public let errors: Int
    public let denied: Int
    public let cached: Int

    enum CodingKeys: String, CodingKey {
        case agentId = "agent_id"
        case callCount = "call_count"
        case errors
        case denied
        case cached
    }
}

public struct MobileControlPlaneCostTopServer: Decodable, Sendable {
    public let server: String
    public let callCount: Int
    public let errors: Int

    enum CodingKeys: String, CodingKey {
        case server
        case callCount = "call_count"
        case errors
    }
}

public struct MobileControlPlaneCost: Decodable, Sendable {
    public let enabled: Bool
    public let timestamp: String?
    public let totalCalls: Int
    public let totalErrors: Int
    public let totalDenied: Int
    public let totalCached: Int
    public let totalDurationMs: Int
    public let topAgent: MobileControlPlaneCostTopAgent?
    public let topServer: MobileControlPlaneCostTopServer?

    enum CodingKeys: String, CodingKey {
        case enabled
        case timestamp
        case totalCalls = "total_calls"
        case totalErrors = "total_errors"
        case totalDenied = "total_denied"
        case totalCached = "total_cached"
        case totalDurationMs = "total_duration_ms"
        case topAgent = "top_agent"
        case topServer = "top_server"
    }
}

public struct MobileControlPlaneRBAC: Decodable, Sendable {
    public let enabled: Bool
    public let defaultPolicy: String?
    public let roleCount: Int
    public let bindingCount: Int
    public let globalDenyCount: Int
    public let rateLimitCount: Int
    public let deniedCount: Int

    enum CodingKeys: String, CodingKey {
        case enabled
        case defaultPolicy = "default_policy"
        case roleCount = "role_count"
        case bindingCount = "binding_count"
        case globalDenyCount = "global_deny_count"
        case rateLimitCount = "rate_limit_count"
        case deniedCount = "denied_count"
    }
}

public struct MobileControlPlaneOTel: Decodable, Sendable {
    public let otlpConfigured: Bool
    public let otlpEndpoint: String?
    public let jsonLogsEnabled: Bool
    public let tracedServers: Int
    public let totalServers: Int
    public let traceCoverage: String?

    enum CodingKeys: String, CodingKey {
        case otlpConfigured = "otlp_configured"
        case otlpEndpoint = "otlp_endpoint"
        case jsonLogsEnabled = "json_logs_enabled"
        case tracedServers = "traced_servers"
        case totalServers = "total_servers"
        case traceCoverage = "trace_coverage"
    }
}

public struct MobileControlPlaneHealth: Decodable, Sendable {
    public let totalServers: Int
    public let healthyServers: Int
    public let degradedServers: Int
    public let downServers: Int
    public let idleServers: Int
    public let hubTargets: Int
    public let localTargets: Int
    public let unavailableTargets: Int

    enum CodingKeys: String, CodingKey {
        case totalServers = "total_servers"
        case healthyServers = "healthy_servers"
        case degradedServers = "degraded_servers"
        case downServers = "down_servers"
        case idleServers = "idle_servers"
        case hubTargets = "hub_targets"
        case localTargets = "local_targets"
        case unavailableTargets = "unavailable_targets"
    }
}

public struct MobileControlPlaneResponse: Decodable, Sendable {
    public let cost: MobileControlPlaneCost
    public let rbac: MobileControlPlaneRBAC
    public let otel: MobileControlPlaneOTel
    public let health: MobileControlPlaneHealth
}

// MARK: - Sandbox / Devbox

public struct MobileSandboxProject: Decodable, Sendable, Identifiable {
    public let project: String
    public let status: String
    public let agentId: String
    public let uptime: String
    public let backend: String

    public var id: String { project + "-" + agentId }

    enum CodingKeys: String, CodingKey {
        case project
        case status
        case agentId = "agent_id"
        case uptime
        case backend
    }

    public init(project: String, status: String, agentId: String, uptime: String, backend: String) {
        self.project = project
        self.status = status
        self.agentId = agentId
        self.uptime = uptime
        self.backend = backend
    }
}

public struct MobileSandboxSummary: Decodable, Sendable {
    public let available: Bool
    public let projects: [MobileSandboxProject]
    public let totalRunning: Int
    public let backend: String

    enum CodingKeys: String, CodingKey {
        case available
        case projects
        case totalRunning = "total_running"
        case backend
    }

    public init(available: Bool, projects: [MobileSandboxProject], totalRunning: Int, backend: String) {
        self.available = available
        self.projects = projects
        self.totalRunning = totalRunning
        self.backend = backend
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        available = (try? container.decode(Bool.self, forKey: .available)) ?? false
        projects = (try? container.decode([MobileSandboxProject].self, forKey: .projects)) ?? []
        totalRunning = (try? container.decode(Int.self, forKey: .totalRunning)) ?? 0
        backend = (try? container.decode(String.self, forKey: .backend)) ?? "unknown"
    }
}

public struct MobileSandboxStartResponse: Decodable, Sendable {
    public let started: Bool
    public let project: String

    public init(started: Bool, project: String) {
        self.started = started
        self.project = project
    }
}

public struct MobileSandboxStopResponse: Decodable, Sendable {
    public let stopped: Bool
    public let project: String

    public init(stopped: Bool, project: String) {
        self.stopped = stopped
        self.project = project
    }
}

// MARK: - Handoff Models

public struct MobileHandoff: Decodable, Identifiable, Sendable {
    public let id: String
    public let fromAgent: String
    public let toAgent: String
    public let targetAgentId: String
    public let status: String
    public let summary: String
    public let context: String
    public let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case fromAgent = "from_agent"
        case toAgent = "to_agent"
        case targetAgentId = "target_agent_id"
        case status
        case summary
        case context
        case createdAt = "created_at"
    }
}

public struct MobileHandoffsResponse: Decodable, Sendable {
    public let handoffs: [MobileHandoff]
    public let total: Int
}

// MARK: - Pipeline Models

public struct MobilePipeline: Decodable, Identifiable, Sendable {
    public var id: Int
    public let project: String
    public let ref: String
    public let status: String
    public let source: String?
    public let createdAt: String
    public let webURL: String?
    public let currentStage: String?
    public let completedStages: Int
    public let totalStages: Int
    public let failedJobCount: Int

    enum CodingKeys: String, CodingKey {
        case id
        case project
        case ref
        case status
        case source
        case createdAt = "created_at"
        case webURL = "web_url"
        case currentStage = "current_stage"
        case completedStages = "completed_stages"
        case totalStages = "total_stages"
        case failedJobCount = "failed_job_count"
    }
}

public struct MobilePipelinesResponse: Decodable, Sendable {
    public let pipelines: [MobilePipeline]
    public let available: Bool
}
