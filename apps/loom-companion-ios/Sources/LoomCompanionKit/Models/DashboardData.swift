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
    public let recentTimeline: [TimelineEntry]
    public let lastHeartbeat: LastHeartbeat?

    public init(daemonRunning: Bool, serverCount: Int, activeSessions: Int, activeAgents: Int, idleAgents: Int, offlineAgents: Int, updatedAt: String, health: HealthSummary, recentTimeline: [TimelineEntry], lastHeartbeat: LastHeartbeat? = nil) {
        self.daemonRunning = daemonRunning
        self.serverCount = serverCount
        self.activeSessions = activeSessions
        self.activeAgents = activeAgents
        self.idleAgents = idleAgents
        self.offlineAgents = offlineAgents
        self.updatedAt = updatedAt
        self.health = health
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
        case recentTimeline = "recent_timeline"
        case lastHeartbeat = "last_heartbeat"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.daemonRunning = try container.decodeIfPresent(Bool.self, forKey: .daemonRunning) ?? false
        self.serverCount = try container.decodeIfPresent(Int.self, forKey: .serverCount) ?? 0
        self.activeSessions = try container.decodeIfPresent(Int.self, forKey: .activeSessions) ?? 0
        self.activeAgents = try container.decodeIfPresent(Int.self, forKey: .activeAgents) ?? 0
        self.idleAgents = try container.decodeIfPresent(Int.self, forKey: .idleAgents) ?? 0
        self.offlineAgents = try container.decodeIfPresent(Int.self, forKey: .offlineAgents) ?? 0
        self.updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        self.health = try container.decodeIfPresent(HealthSummary.self, forKey: .health) ?? HealthSummary(totalServers: 0, healthyServers: 0, degradedServers: 0, downServers: 0, idleServers: 0)
        self.recentTimeline = try container.decodeIfPresent([TimelineEntry].self, forKey: .recentTimeline) ?? []
        self.lastHeartbeat = try container.decodeIfPresent(LastHeartbeat.self, forKey: .lastHeartbeat)
    }
}
