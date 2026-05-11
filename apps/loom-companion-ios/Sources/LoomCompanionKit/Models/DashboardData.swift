import Foundation

public struct LastHeartbeat: Decodable, Sendable {
    public let agentId: String
    public let timestamp: String
    public let count1h: Int

    enum CodingKeys: String, CodingKey {
        case agentId = "agent_id"
        case timestamp
        case count1h = "count_1h"
    }

    public init(agentId: String = "", timestamp: String = "", count1h: Int = 0) {
        self.agentId = agentId
        self.timestamp = timestamp
        self.count1h = count1h
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.agentId = try container.decodeIfPresent(String.self, forKey: .agentId) ?? ""
        self.timestamp = try container.decodeIfPresent(String.self, forKey: .timestamp) ?? ""
        self.count1h = try container.decodeIfPresent(Int.self, forKey: .count1h) ?? 0
    }
}

public struct DashboardCoordinationSummary: Decodable, Sendable, Hashable {
    public let activeNamespaces: Int
    public let namespacesAtRisk: Int
    public let agentsNeedingAttention: Int
    public let sharedBranches: Int
    public let conflictFiles: Int
    public let crossAgentBlockers: Int
    public let orphanTasks: Int
    public let idleClaimHolders: Int
    public let mergeReadyBranches: Int

    enum CodingKeys: String, CodingKey {
        case activeNamespaces = "active_namespaces"
        case namespacesAtRisk = "namespaces_at_risk"
        case agentsNeedingAttention = "agents_needing_attention"
        case sharedBranches = "shared_branches"
        case conflictFiles = "conflict_files"
        case crossAgentBlockers = "cross_agent_blockers"
        case orphanTasks = "orphan_tasks"
        case idleClaimHolders = "idle_claim_holders"
        case mergeReadyBranches = "merge_ready_branches"
    }

    public init(
        activeNamespaces: Int = 0,
        namespacesAtRisk: Int = 0,
        agentsNeedingAttention: Int = 0,
        sharedBranches: Int = 0,
        conflictFiles: Int = 0,
        crossAgentBlockers: Int = 0,
        orphanTasks: Int = 0,
        idleClaimHolders: Int = 0,
        mergeReadyBranches: Int = 0
    ) {
        self.activeNamespaces = activeNamespaces
        self.namespacesAtRisk = namespacesAtRisk
        self.agentsNeedingAttention = agentsNeedingAttention
        self.sharedBranches = sharedBranches
        self.conflictFiles = conflictFiles
        self.crossAgentBlockers = crossAgentBlockers
        self.orphanTasks = orphanTasks
        self.idleClaimHolders = idleClaimHolders
        self.mergeReadyBranches = mergeReadyBranches
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.activeNamespaces = try container.decodeIfPresent(Int.self, forKey: .activeNamespaces) ?? 0
        self.namespacesAtRisk = try container.decodeIfPresent(Int.self, forKey: .namespacesAtRisk) ?? 0
        self.agentsNeedingAttention = try container.decodeIfPresent(Int.self, forKey: .agentsNeedingAttention) ?? 0
        self.sharedBranches = try container.decodeIfPresent(Int.self, forKey: .sharedBranches) ?? 0
        self.conflictFiles = try container.decodeIfPresent(Int.self, forKey: .conflictFiles) ?? 0
        self.crossAgentBlockers = try container.decodeIfPresent(Int.self, forKey: .crossAgentBlockers) ?? 0
        self.orphanTasks = try container.decodeIfPresent(Int.self, forKey: .orphanTasks) ?? 0
        self.idleClaimHolders = try container.decodeIfPresent(Int.self, forKey: .idleClaimHolders) ?? 0
        self.mergeReadyBranches = try container.decodeIfPresent(Int.self, forKey: .mergeReadyBranches) ?? 0
    }
}

public struct DashboardAttentionLane: Decodable, Sendable, Hashable {
    public struct Filter: Decodable, Sendable, Hashable {
        public let status: String?
        public let agentId: String?
        public let sessionId: String?
        public let namespace: String?

        enum CodingKeys: String, CodingKey {
            case status
            case agentId = "agent_id"
            case sessionId = "session_id"
            case namespace
        }

        public init(status: String? = nil, agentId: String? = nil, sessionId: String? = nil, namespace: String? = nil) {
            self.status = status
            self.agentId = agentId
            self.sessionId = sessionId
            self.namespace = namespace
        }

        public init(from decoder: any Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            self.status = try container.decodeIfPresent(String.self, forKey: .status)
            self.agentId = try container.decodeIfPresent(String.self, forKey: .agentId)
            self.sessionId = try container.decodeIfPresent(String.self, forKey: .sessionId)
            self.namespace = try container.decodeIfPresent(String.self, forKey: .namespace)
        }
    }

    public struct Freshness: Decodable, Sendable, Hashable {
        public let source: String
        public let updatedAt: String?

        enum CodingKeys: String, CodingKey {
            case source
            case updatedAt = "updated_at"
        }

        public init(source: String = "", updatedAt: String? = nil) {
            self.source = source
            self.updatedAt = updatedAt
        }

        public init(from decoder: any Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            self.source = try container.decodeIfPresent(String.self, forKey: .source) ?? ""
            self.updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
        }
    }

    public let type: String
    public let id: String
    public let label: String
    public let route: String
    public let scope: String
    public let summary: String
    public let severity: String
    public let targetKind: String
    public let targetId: String
    public let deepLink: String
    public let filter: Filter?
    public let recommendedAction: String
    public let freshness: Freshness?

    public var stableID: String { "\(type):\(id)" }

    public var isTaskLane: Bool {
        if targetKind == "task_filter" { return true }
        if deepLink.hasPrefix("loom://tasks") { return true }

        switch type {
        case "blocked_task", "blocker", "orphan_task", "task", "task_filter":
            return true
        default:
            break
        }

        return laneSearchText.contains("blocked task")
            || laneSearchText.contains("blocked tasks")
            || laneSearchText.contains("orphan task")
            || laneSearchText.contains("orphan tasks")
            || laneSearchText.contains("task filter")
    }

    public var taskStatusHint: String? {
        if filter?.status?.lowercased() == "blocked" { return "blocked" }
        if laneSearchText.contains("blocked task") || laneSearchText.contains("blocked tasks") {
            return "blocked"
        }
        return filter?.status
    }

    private var laneSearchText: String {
        [
            type,
            label,
            summary,
            scope,
            targetKind,
            deepLink,
            recommendedAction
        ]
        .joined(separator: " ")
        .lowercased()
    }

    enum CodingKeys: String, CodingKey {
        case type, id, label, route, scope, summary, severity
        case targetKind = "target_kind"
        case targetId = "target_id"
        case deepLink = "deep_link"
        case filter
        case recommendedAction = "recommended_action"
        case freshness
    }

    public init(
        type: String,
        id: String,
        label: String,
        route: String,
        scope: String,
        summary: String,
        severity: String = "info",
        targetKind: String = "",
        targetId: String = "",
        deepLink: String = "",
        filter: Filter? = nil,
        recommendedAction: String = "",
        freshness: Freshness? = nil
    ) {
        self.type = type
        self.id = id
        self.label = label
        self.route = route
        self.scope = scope
        self.summary = summary
        self.severity = severity
        self.targetKind = targetKind
        self.targetId = targetId
        self.deepLink = deepLink
        self.filter = filter
        self.recommendedAction = recommendedAction
        self.freshness = freshness
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.type = try container.decodeIfPresent(String.self, forKey: .type) ?? ""
        self.id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        self.label = try container.decodeIfPresent(String.self, forKey: .label) ?? ""
        self.route = try container.decodeIfPresent(String.self, forKey: .route) ?? ""
        self.scope = try container.decodeIfPresent(String.self, forKey: .scope) ?? ""
        self.summary = try container.decodeIfPresent(String.self, forKey: .summary) ?? ""
        self.severity = try container.decodeIfPresent(String.self, forKey: .severity) ?? "info"
        self.targetKind = try container.decodeIfPresent(String.self, forKey: .targetKind) ?? ""
        self.targetId = try container.decodeIfPresent(String.self, forKey: .targetId) ?? ""
        self.deepLink = try container.decodeIfPresent(String.self, forKey: .deepLink) ?? ""
        self.filter = try container.decodeIfPresent(Filter.self, forKey: .filter)
        self.recommendedAction = try container.decodeIfPresent(String.self, forKey: .recommendedAction) ?? ""
        self.freshness = try container.decodeIfPresent(Freshness.self, forKey: .freshness)
    }
}

public struct DashboardCoordination: Decodable, Sendable, Hashable {
    public let summary: DashboardCoordinationSummary
    public let attentionLanes: [DashboardAttentionLane]

    enum CodingKeys: String, CodingKey {
        case summary
        case attentionLanes = "attention_lanes"
    }

    public init(
        summary: DashboardCoordinationSummary = DashboardCoordinationSummary(
            activeNamespaces: 0,
            namespacesAtRisk: 0,
            agentsNeedingAttention: 0,
            sharedBranches: 0,
            conflictFiles: 0,
            crossAgentBlockers: 0,
            orphanTasks: 0,
            idleClaimHolders: 0,
            mergeReadyBranches: 0
        ),
        attentionLanes: [DashboardAttentionLane] = []
    ) {
        self.summary = summary
        self.attentionLanes = attentionLanes
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.summary = (try? container.decodeIfPresent(DashboardCoordinationSummary.self, forKey: .summary)) ?? DashboardCoordinationSummary()
        self.attentionLanes = (try? container.decodeIfPresent([DashboardAttentionLane].self, forKey: .attentionLanes)) ?? []
    }
}

/// Dashboard aggregate matching the mobile v1 dashboard endpoint response.
public struct DashboardData: Decodable, Sendable {
    public let daemonRunning: Bool
    public let serverCount: Int
    public let activeSessions: Int
    public let activeAgents: Int
    public let idleAgents: Int
    public let offlineAgents: Int
    public let updatedAt: String
    public let health: HealthSummary
    public let coordination: DashboardCoordination
    public let recentTimeline: [TimelineEntry]
    public let lastHeartbeat: LastHeartbeat?

    public init(daemonRunning: Bool, serverCount: Int, activeSessions: Int, activeAgents: Int, idleAgents: Int, offlineAgents: Int, updatedAt: String, health: HealthSummary, coordination: DashboardCoordination = DashboardCoordination(), recentTimeline: [TimelineEntry], lastHeartbeat: LastHeartbeat? = nil) {
        self.daemonRunning = daemonRunning
        self.serverCount = serverCount
        self.activeSessions = activeSessions
        self.activeAgents = activeAgents
        self.idleAgents = idleAgents
        self.offlineAgents = offlineAgents
        self.updatedAt = updatedAt
        self.health = health
        self.coordination = coordination
        self.recentTimeline = recentTimeline
        self.lastHeartbeat = lastHeartbeat
    }

    enum CodingKeys: String, CodingKey {
        case daemonRunning = "daemon_running"
        case serverCount = "server_count"
        case activeSessions = "active_sessions"
        case activeAgents = "active_agents"
        case idleAgents = "idle_agents"
        case offlineAgents = "offline_agents"
        case updatedAt = "updated_at"
        case health
        case coordination
        case recentTimeline = "recent_timeline"
        case lastHeartbeat = "last_heartbeat"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.daemonRunning = (try? container.decodeIfPresent(Bool.self, forKey: .daemonRunning)) ?? false
        self.serverCount = (try? container.decodeIfPresent(Int.self, forKey: .serverCount)) ?? 0
        self.activeSessions = (try? container.decodeIfPresent(Int.self, forKey: .activeSessions)) ?? 0
        self.activeAgents = (try? container.decodeIfPresent(Int.self, forKey: .activeAgents)) ?? 0
        self.idleAgents = (try? container.decodeIfPresent(Int.self, forKey: .idleAgents)) ?? 0
        self.offlineAgents = (try? container.decodeIfPresent(Int.self, forKey: .offlineAgents)) ?? 0
        self.updatedAt = (try? container.decodeIfPresent(String.self, forKey: .updatedAt)) ?? ""
        self.health = (try? container.decodeIfPresent(HealthSummary.self, forKey: .health)) ?? HealthSummary(totalServers: 0, healthyServers: 0, degradedServers: 0, downServers: 0, idleServers: 0)
        self.coordination = (try? container.decodeIfPresent(DashboardCoordination.self, forKey: .coordination)) ?? DashboardCoordination()
        self.recentTimeline = (try? container.decodeIfPresent([TimelineEntry].self, forKey: .recentTimeline)) ?? []
        self.lastHeartbeat = try? container.decodeIfPresent(LastHeartbeat.self, forKey: .lastHeartbeat)
    }
}
