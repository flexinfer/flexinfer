import Foundation

// MARK: - Shared widget data models

/// Data snapshot shared between the main app and WidgetKit extension via App Group UserDefaults.
public struct WidgetData: Codable, Sendable {
    public let fleet: FleetWidgetData
    public let tasks: TaskWidgetData
    public let sessions: SessionWidgetData
    public let lastCompletedSession: CompletedSessionWidgetData?
    public let lastUpdated: Date

    public init(
        fleet: FleetWidgetData,
        tasks: TaskWidgetData,
        sessions: SessionWidgetData,
        lastCompletedSession: CompletedSessionWidgetData? = nil,
        lastUpdated: Date = .now
    ) {
        self.fleet = fleet
        self.tasks = tasks
        self.sessions = sessions
        self.lastCompletedSession = lastCompletedSession
        self.lastUpdated = lastUpdated
    }
}

public struct FleetWidgetData: Codable, Sendable {
    public let daemonRunning: Bool
    public let serverCount: Int
    public let sessionCount: Int
    public let activeAgents: Int
    public let idleAgents: Int
    public let offlineAgents: Int
    public let healthyServers: Int
    public let degradedServers: Int
    public let downServers: Int

    public init(daemonRunning: Bool, serverCount: Int, sessionCount: Int, activeAgents: Int, idleAgents: Int, offlineAgents: Int, healthyServers: Int, degradedServers: Int, downServers: Int) {
        self.daemonRunning = daemonRunning
        self.serverCount = serverCount
        self.sessionCount = sessionCount
        self.activeAgents = activeAgents
        self.idleAgents = idleAgents
        self.offlineAgents = offlineAgents
        self.healthyServers = healthyServers
        self.degradedServers = degradedServers
        self.downServers = downServers
    }
}

public struct TaskWidgetData: Codable, Sendable {
    public let pending: Int
    public let inProgress: Int
    public let blocked: Int
    public let completed: Int
    public let recentTitles: [String]

    public init(pending: Int, inProgress: Int, blocked: Int, completed: Int, recentTitles: [String]) {
        self.pending = pending
        self.inProgress = inProgress
        self.blocked = blocked
        self.completed = completed
        self.recentTitles = recentTitles
    }
}

public struct SessionWidgetData: Codable, Sendable {
    public let activeCount: Int
    public let topSessions: [SessionWidgetEntry]

    public init(activeCount: Int, topSessions: [SessionWidgetEntry]) {
        self.activeCount = activeCount
        self.topSessions = topSessions
    }
}

public struct SessionWidgetEntry: Codable, Sendable, Identifiable {
    public let id: String
    public let namespace: String
    public let agentId: String
    public let startedAt: String

    public init(id: String, namespace: String, agentId: String, startedAt: String) {
        self.id = id
        self.namespace = namespace
        self.agentId = agentId
        self.startedAt = startedAt
    }
}

public struct CompletedSessionWidgetData: Codable, Sendable {
    public let agentId: String
    public let agentType: String
    public let namespace: String
    public let durationSeconds: Int
    public let tokenCount: Int
    public let entryCount: Int
    public let endedAt: String

    public init(agentId: String, agentType: String, namespace: String, durationSeconds: Int, tokenCount: Int, entryCount: Int, endedAt: String) {
        self.agentId = agentId
        self.agentType = agentType
        self.namespace = namespace
        self.durationSeconds = durationSeconds
        self.tokenCount = tokenCount
        self.entryCount = entryCount
        self.endedAt = endedAt
    }
}

// MARK: - App Group Data Store

public enum SharedDataStore {
    public static let appGroupID = "group.ai.flexinfer.loom.companion"
    private static let widgetDataKey = "widgetData"

    public static func save(_ data: WidgetData) {
        guard let defaults = UserDefaults(suiteName: appGroupID),
              let encoded = try? JSONEncoder().encode(data) else { return }
        defaults.set(encoded, forKey: widgetDataKey)
    }

    public static func load() -> WidgetData? {
        guard let defaults = UserDefaults(suiteName: appGroupID),
              let data = defaults.data(forKey: widgetDataKey),
              let decoded = try? JSONDecoder().decode(WidgetData.self, from: data) else { return nil }
        return decoded
    }

    public static var placeholder: WidgetData {
        WidgetData(
            fleet: FleetWidgetData(daemonRunning: true, serverCount: 12, sessionCount: 3, activeAgents: 2, idleAgents: 1, offlineAgents: 0, healthyServers: 10, degradedServers: 1, downServers: 0),
            tasks: TaskWidgetData(pending: 3, inProgress: 2, blocked: 1, completed: 8, recentTitles: ["Implement auth flow", "Fix SSE reconnect"]),
            sessions: SessionWidgetData(activeCount: 3, topSessions: [
                SessionWidgetEntry(id: "s1", namespace: "loom-core/feature", agentId: "claude-code", startedAt: "10m ago"),
            ])
        )
    }
}
