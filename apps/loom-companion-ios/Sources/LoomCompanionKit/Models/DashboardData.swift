import Foundation

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

    public init(daemonRunning: Bool, serverCount: Int, activeSessions: Int, activeAgents: Int, idleAgents: Int, offlineAgents: Int, updatedAt: String, health: HealthSummary, recentTimeline: [TimelineEntry]) {
        self.daemonRunning = daemonRunning
        self.serverCount = serverCount
        self.activeSessions = activeSessions
        self.activeAgents = activeAgents
        self.idleAgents = idleAgents
        self.offlineAgents = offlineAgents
        self.updatedAt = updatedAt
        self.health = health
        self.recentTimeline = recentTimeline
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
    }
}
