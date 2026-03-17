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

    /// Agents after applying current filters.
    public var filteredAgents: [UnifiedAgent] {
        agents.filter { agent in
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
                    || (agent.namespace?.lowercased().contains(query) ?? false)
                if !matches { return false }
            }
            return true
        }
    }

    /// Unique agent types from current agents, for filter dropdown.
    public var availableTypes: [String] {
        Array(Set(agents.map(\.agentType))).sorted()
    }
}
