import Foundation

// MARK: - Shared widget data models

/// Data snapshot shared between the main app and WidgetKit extension via App Group UserDefaults.
public struct WidgetData: Codable, Sendable {
    public let fleet: FleetWidgetData
    public let tasks: TaskWidgetData
    public let sessions: SessionWidgetData
    public let attentionLanes: [AttentionLaneWidgetEntry]
    public let lastCompletedSession: CompletedSessionWidgetData?
    public let lastUpdated: Date

    public init(
        fleet: FleetWidgetData,
        tasks: TaskWidgetData,
        sessions: SessionWidgetData,
        attentionLanes: [AttentionLaneWidgetEntry] = [],
        lastCompletedSession: CompletedSessionWidgetData? = nil,
        lastUpdated: Date = .now
    ) {
        self.fleet = fleet
        self.tasks = tasks
        self.sessions = sessions
        self.attentionLanes = attentionLanes
        self.lastCompletedSession = lastCompletedSession
        self.lastUpdated = lastUpdated
    }

    enum CodingKeys: String, CodingKey {
        case fleet, tasks, sessions, attentionLanes, lastCompletedSession, lastUpdated
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.fleet = try c.decode(FleetWidgetData.self, forKey: .fleet)
        self.tasks = try c.decode(TaskWidgetData.self, forKey: .tasks)
        self.sessions = try c.decode(SessionWidgetData.self, forKey: .sessions)
        self.attentionLanes = try c.decodeIfPresent([AttentionLaneWidgetEntry].self, forKey: .attentionLanes) ?? []
        self.lastCompletedSession = try c.decodeIfPresent(CompletedSessionWidgetData.self, forKey: .lastCompletedSession)
        self.lastUpdated = try c.decodeIfPresent(Date.self, forKey: .lastUpdated) ?? .now
    }
}

public struct AttentionLaneWidgetEntry: Codable, Sendable, Hashable, Identifiable {
    public let type: String
    public let laneID: String
    public let label: String
    public let route: String
    public let scope: String
    public let summary: String
    public let severity: String

    public var id: String { "\(type):\(laneID)" }

    public init(type: String, laneID: String, label: String, route: String, scope: String, summary: String, severity: String) {
        self.type = type
        self.laneID = laneID
        self.label = label
        self.route = route
        self.scope = scope
        self.summary = summary
        self.severity = severity
    }

    enum CodingKeys: String, CodingKey {
        case type
        case laneID = "id"
        case label, route, scope, summary, severity
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
    public let agentType: String
    public let startedAt: String
    public let lastHeartbeat: Date?

    public init(id: String, namespace: String, agentId: String, agentType: String = "", startedAt: String, lastHeartbeat: Date? = nil) {
        self.id = id
        self.namespace = namespace
        self.agentId = agentId
        self.agentType = agentType
        self.startedAt = startedAt
        self.lastHeartbeat = lastHeartbeat
    }

    /// Whether the session had a heartbeat within the last 30 seconds.
    public var isRecentlyActive: Bool {
        guard let hb = lastHeartbeat else { return false }
        return Date().timeIntervalSince(hb) < 30
    }
}

/// Stable widget-kind identifier for the SpawnBudgetWidget. Shared between
/// the widget extension (`StaticConfiguration(kind:)`) and the SpawnViewModel
/// (`WidgetCenter.reloadTimelines(ofKind:)`) so timeline reloads target only
/// this widget instead of refreshing every widget on every spawn delta.
public let SpawnBudgetWidgetKind = "SpawnBudgetWidget"

// MARK: - Spawn Budget Widget Data

/// Snapshot of currently-active headless agent spawns, surfaced to the
/// SpawnBudgetWidget so operators can see live cost/turn pressure on the
/// home screen without opening the app. Written separately from the main
/// `WidgetData` blob (see `SharedDataStore.saveSpawnBudget`) because the
/// SpawnViewModel is the only writer and runs on a different cadence than
/// the dashboard sync.
public struct SpawnBudgetWidgetData: Codable, Sendable, Equatable, Hashable {
    public let entries: [SpawnBudgetWidgetEntry]
    public let lastUpdated: Date

    public init(entries: [SpawnBudgetWidgetEntry], lastUpdated: Date = .now) {
        self.entries = entries
        self.lastUpdated = lastUpdated
    }

    enum CodingKeys: String, CodingKey {
        case entries
        case lastUpdated
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.entries = try c.decodeIfPresent([SpawnBudgetWidgetEntry].self, forKey: .entries) ?? []
        self.lastUpdated = try c.decodeIfPresent(Date.self, forKey: .lastUpdated) ?? .now
    }
}

/// Per-spawn budget snapshot row. Cost/turn caps are optional because
/// operators may spawn without explicit budgets — in that case the widget
/// renders the running total without a progress bar.
public struct SpawnBudgetWidgetEntry: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let spawnId: String
    public let agentType: String
    public let namespace: String
    public let status: String
    public let totalCostUSD: Double
    public let costEstimated: Bool
    public let maxCostUSD: Double?
    public let turnCount: Int
    public let maxTurns: Int?
    public let startedAt: String

    public var id: String { spawnId }

    /// Fraction of cost budget consumed (0.0 - 1.0). Returns nil when there
    /// is no `maxCostUSD` cap or the cap is non-positive.
    public var costFraction: Double? {
        guard let cap = maxCostUSD, cap > 0 else { return nil }
        return min(totalCostUSD / cap, 1.0)
    }

    /// Fraction of turn budget consumed (0.0 - 1.0). Returns nil when there
    /// is no `maxTurns` cap or the cap is non-positive.
    public var turnFraction: Double? {
        guard let cap = maxTurns, cap > 0 else { return nil }
        return min(Double(turnCount) / Double(cap), 1.0)
    }

    public init(
        spawnId: String,
        agentType: String,
        namespace: String,
        status: String,
        totalCostUSD: Double,
        costEstimated: Bool = false,
        maxCostUSD: Double? = nil,
        turnCount: Int,
        maxTurns: Int? = nil,
        startedAt: String
    ) {
        self.spawnId = spawnId
        self.agentType = agentType
        self.namespace = namespace
        self.status = status
        self.totalCostUSD = totalCostUSD
        self.costEstimated = costEstimated
        self.maxCostUSD = maxCostUSD
        self.turnCount = turnCount
        self.maxTurns = maxTurns
        self.startedAt = startedAt
    }

    enum CodingKeys: String, CodingKey {
        case spawnId = "spawn_id"
        case agentType = "agent_type"
        case namespace
        case status
        case totalCostUSD = "total_cost_usd"
        case costEstimated = "cost_estimated"
        case maxCostUSD = "max_cost_usd"
        case turnCount = "turn_count"
        case maxTurns = "max_turns"
        case startedAt = "started_at"
    }
}

extension SpawnBudgetWidgetData {
    /// Derive a widget snapshot from the SpawnViewModel's live state. Picks
    /// the top-N currently-active spawns ordered by total cost desc (so the
    /// most budget pressure is always visible). Spawns without telemetry get
    /// a zero-cost entry so the widget can still show "spawn started, no cost
    /// yet" rather than dropping the row entirely.
    ///
    /// - Parameters:
    ///   - spawns: full list from `SpawnViewModel.spawns`.
    ///   - telemetry: per-spawn telemetry from `SpawnViewModel.telemetryBySpawnID`.
    ///   - limit: max rows (default 5). Pinned to keep widget memory tight.
    public static func from(
        spawns: [MobileSpawnStatus],
        telemetry: [String: SpawnTelemetry],
        limit: Int = 5,
        now: Date = .now
    ) -> SpawnBudgetWidgetData {
        let active = spawns.filter(\.isActive)
        let entries: [SpawnBudgetWidgetEntry] = active.map { status in
            let tele = telemetry[status.spawnId]
            return SpawnBudgetWidgetEntry(
                spawnId: status.spawnId,
                agentType: status.request.agentType,
                namespace: status.request.namespace ?? status.request.project,
                status: status.status,
                totalCostUSD: tele?.totalCostUSD ?? 0,
                costEstimated: tele?.costEstimated ?? false,
                maxCostUSD: status.request.maxCostUSD,
                turnCount: tele?.turnCount ?? 0,
                maxTurns: status.request.maxTurns,
                startedAt: status.startedAt
            )
        }
        let ranked = entries.sorted { lhs, rhs in
            if lhs.totalCostUSD != rhs.totalCostUSD {
                return lhs.totalCostUSD > rhs.totalCostUSD
            }
            // Tie-break on turn count, then spawn id for determinism.
            if lhs.turnCount != rhs.turnCount {
                return lhs.turnCount > rhs.turnCount
            }
            return lhs.spawnId < rhs.spawnId
        }
        return SpawnBudgetWidgetData(
            entries: Array(ranked.prefix(max(limit, 0))),
            lastUpdated: now
        )
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
    private static let spawnBudgetKey = "spawnBudgetData"

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

    /// Persist the live spawn-budget snapshot for the SpawnBudgetWidget.
    /// Written separately from the main `WidgetData` blob so SpawnViewModel
    /// can publish on its own cadence without contending with DashboardViewModel.
    public static func saveSpawnBudget(_ data: SpawnBudgetWidgetData) {
        guard let defaults = UserDefaults(suiteName: appGroupID),
              let encoded = try? JSONEncoder().encode(data) else { return }
        defaults.set(encoded, forKey: spawnBudgetKey)
    }

    /// Load the spawn-budget snapshot. Returns nil when no spawn has ever
    /// been written; callers should fall back to `placeholderSpawnBudget`.
    public static func loadSpawnBudget() -> SpawnBudgetWidgetData? {
        guard let defaults = UserDefaults(suiteName: appGroupID),
              let data = defaults.data(forKey: spawnBudgetKey),
              let decoded = try? JSONDecoder().decode(SpawnBudgetWidgetData.self, from: data) else { return nil }
        return decoded
    }

    public static var placeholderSpawnBudget: SpawnBudgetWidgetData {
        SpawnBudgetWidgetData(entries: [
            SpawnBudgetWidgetEntry(
                spawnId: "spawn-placeholder-1",
                agentType: "claude-code",
                namespace: "loom-core/feature",
                status: "running",
                totalCostUSD: 0.42,
                costEstimated: false,
                maxCostUSD: 1.0,
                turnCount: 6,
                maxTurns: 20,
                startedAt: "5m ago"
            ),
            SpawnBudgetWidgetEntry(
                spawnId: "spawn-placeholder-2",
                agentType: "codex",
                namespace: "platform/gitops",
                status: "running",
                totalCostUSD: 0.08,
                costEstimated: true,
                maxCostUSD: nil,
                turnCount: 2,
                maxTurns: nil,
                startedAt: "1m ago"
            ),
        ])
    }

    public static var placeholder: WidgetData {
        WidgetData(
            fleet: FleetWidgetData(daemonRunning: true, serverCount: 12, sessionCount: 3, activeAgents: 2, idleAgents: 1, offlineAgents: 0, healthyServers: 10, degradedServers: 1, downServers: 0),
            tasks: TaskWidgetData(pending: 3, inProgress: 2, blocked: 1, completed: 8, recentTitles: ["Implement auth flow", "Fix SSE reconnect"]),
            sessions: SessionWidgetData(activeCount: 3, topSessions: [
                SessionWidgetEntry(id: "s1", namespace: "loom-core/feature", agentId: "claude-code", agentType: "claude-code", startedAt: "10m ago", lastHeartbeat: Date()),
            ]),
            attentionLanes: [
                AttentionLaneWidgetEntry(
                    type: "namespace",
                    laneID: "loom-core/mobile",
                    label: "Work lane",
                    route: "work",
                    scope: "3 tasks",
                    summary: "blocked tasks",
                    severity: "critical"
                ),
            ]
        )
    }
}
