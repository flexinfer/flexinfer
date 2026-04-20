import Foundation

/// ViewModel for the unified Agents tab.
@Observable
public final class AgentsViewModel {
    public var agents: [UnifiedAgent] = []
    public var summary: UnifiedAgentsSummary?
    public var isLoading = false
    public var error: LoomAPIError?

    // Filters
    public var statusFilter: MobilePresenceStatus?
    public var typeFilter: String?
    public var searchText: String = ""
    public var attentionOnly: Bool = false

    // Session mutation state
    public var isCreating = false
    public var createError: String?

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    @ObservationIgnored
    private var sseRegistrationId: UUID?

    /// SSE event types that trigger an agents refresh.
    private static let refreshEventTypes: Set<String> = [
        "hud.fleet", "agent.heartbeat",
        "agent.session.start", "agent.session.end", "agent.session.reaped",
        "agent.spawn.building", "agent.spawn.running",
        "agent.spawn.completed", "agent.spawn.failed", "agent.spawn.stopped",
        "agent.context.added", "agent.session.stats.updated",
        "agent.task.update",
    ]

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    /// Start listening to SSE events via the broadcaster.
    @MainActor
    public func startListening(broadcaster: SSEEventBroadcaster) {
        sseRegistrationId = broadcaster.register { [weak self] event in
            await self?.handleSSEEvent(event)
        }
    }

    /// Stop listening to SSE events.
    @MainActor
    public func stopListening(broadcaster: SSEEventBroadcaster) {
        if let id = sseRegistrationId {
            broadcaster.unregister(id)
            sseRegistrationId = nil
        }
    }

    @MainActor
    private func handleSSEEvent(_ event: SSEEvent) async {
        if Self.refreshEventTypes.contains(event.type) {
            await load()
        }
    }

    /// Fetch unified agents from the API.
    public func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let response: UnifiedAgentsResponse = try await apiClient.request(.agents(limit: 50))
            agents = response.agents
            summary = response.summary
            error = nil
        } catch let err as LoomAPIError {
            error = err
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
        }
    }

    /// Create a new session and reload agents on success.
    public func createSession(
        agentId: String,
        namespace: String? = nil,
        description: String? = nil,
        autoRecall: Bool? = nil
    ) async {
        isCreating = true
        createError = nil
        defer { isCreating = false }

        do {
            let _: SessionCreateResponse = try await apiClient.request(
                .createSession(agentId: agentId, namespace: namespace, description: description, autoRecall: autoRecall)
            )
            await load()
        } catch let err as LoomAPIError {
            createError = err.description
        } catch {
            createError = error.localizedDescription
        }
    }

    /// Total agents flagged as needing attention (ignores current filters).
    public var attentionCount: Int {
        agents.reduce(0) { count, agent in
            count + ((agent.needsAttention || agent.blockedTasks > 0) ? 1 : 0)
        }
    }

    /// Agents after applying current filters.
    public var filteredAgents: [UnifiedAgent] {
        agents.filter { agent in
            if attentionOnly, !(agent.needsAttention || agent.blockedTasks > 0) {
                return false
            }
            if let statusFilter, agent.status != statusFilter {
                return false
            }
            if let typeFilter, !typeFilter.isEmpty, !agent.agentType.localizedCaseInsensitiveContains(typeFilter) {
                return false
            }
            if !searchText.isEmpty {
                let query = searchText.lowercased()
                let matches = agent.agentId.lowercased().contains(query)
                    || agent.description.lowercased().contains(query)
                    || agent.currentTask.lowercased().contains(query)
                    || agent.branch.lowercased().contains(query)
                    || (agent.project?.lowercased().contains(query) ?? false)
                    || (agent.namespace?.lowercased().contains(query) ?? false)
                if !matches { return false }
            }
            return true
        }
    }

    public var groupedAgents: [UnifiedAgentGroup] {
        var grouped: [String: UnifiedAgentGroup] = [:]
        for agent in filteredAgents {
            let descriptor = groupDescriptor(for: agent)
            let existingAgents = grouped[descriptor.id]?.agents ?? []
            grouped[descriptor.id] = UnifiedAgentGroup(
                id: descriptor.id,
                title: descriptor.title,
                subtitle: descriptor.subtitle,
                agents: existingAgents + [agent]
            )
        }

        return grouped.values.sorted { lhs, rhs in
            let leftRank = groupSortRank(lhs.id)
            let rightRank = groupSortRank(rhs.id)
            if leftRank != rightRank {
                return leftRank < rightRank
            }
            if lhs.agents.count != rhs.agents.count {
                return lhs.agents.count > rhs.agents.count
            }
            return lhs.title.localizedCaseInsensitiveCompare(rhs.title) == .orderedAscending
        }
    }

    /// Unique agent types from current agents, for filter dropdown.
    public var availableTypes: [String] {
        Array(Set(agents.map(\.agentType))).sorted()
    }

    private func groupDescriptor(for agent: UnifiedAgent) -> (id: String, title: String, subtitle: String?) {
        if let project = normalized(agent.project) {
            return (
                "project:\(project)",
                project.components(separatedBy: "/").last ?? project,
                project
            )
        }
        if let namespace = normalized(agent.namespace) {
            return (
                "namespace:\(namespace)",
                namespace.components(separatedBy: "/").last ?? namespace,
                "Namespace \(namespace)"
            )
        }
        if let branch = normalized(agent.branch) {
            return (
                "branch:\(branch)",
                branch,
                "Branch group"
            )
        }
        let runtime = displayAgentType(agent.agentType)
        return (
            "runtime:\(runtime.lowercased())",
            runtime,
            "Ungrouped runtime"
        )
    }

    private func groupSortRank(_ id: String) -> Int {
        if id.hasPrefix("project:") { return 0 }
        if id.hasPrefix("namespace:") { return 1 }
        if id.hasPrefix("branch:") { return 2 }
        return 3
    }

    private func normalized(_ value: String?) -> String? {
        guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines),
              !trimmed.isEmpty
        else {
            return nil
        }
        return trimmed
    }

    private func displayAgentType(_ agentType: String) -> String {
        let trimmed = agentType.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return "Runtime" }
        return trimmed
            .replacingOccurrences(of: "_", with: " ")
            .replacingOccurrences(of: "-", with: " ")
            .split(separator: " ")
            .map { $0.capitalized }
            .joined(separator: " ")
    }
}
