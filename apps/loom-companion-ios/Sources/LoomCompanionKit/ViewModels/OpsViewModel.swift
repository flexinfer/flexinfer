import Foundation

/// ViewModel powering the read-only mobile parity "Ops" tab.
@Observable
public final class OpsViewModel {
    public var isLoading = false
    public var error: LoomAPIError?
    public var warningMessage: String?

    public var tasks: [MobileTask] = []
    public var taskCounts = MobileTaskCounts(pending: 0, inProgress: 0, blocked: 0, completed: 0)

    public var workflows: [MobileWorkflow] = []
    public var pendingApprovals = 0

    public var presenceAgents: [MobilePresenceAgent] = []
    public var presenceClaims: [MobileFileClaim] = []
    public var presenceWorktrees: [MobileWorktree] = []
    public var presenceSummary = MobilePresenceSummary(activeAgents: 0, idleAgents: 0, offlineAgents: 0, totalAgents: 0, claimCount: 0, worktreeCount: 0)

    public var memoryStats: MobileMemoryStats?
    public var memoryItems: [MobileMemoryItem] = []
    public var memoryTier: MobileMemoryTier = .working

    public var streamEntries: [MobileStreamEntry] = []

    public var topology: MobileTopologyResponse?
    public var graphStats: MobileGraphStats?
    public var graphEntities: [MobileGraphEntity] = []
    public var graphPath: MobileGraphPath?

    public var reasoningChains: [MobileReasoningChain] = []

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    public func load() async {
        isLoading = true
        defer { isLoading = false }
        error = nil
        warningMessage = nil

        var attemptedSections = 0
        var failedSections: [String] = []
        var firstError: LoomAPIError?

        func markFailure(_ section: String, _ loadError: Error) {
            let typedError = self.toLoomError(loadError)
            if firstError == nil {
                firstError = typedError
            }
            failedSections.append(section)
        }

        attemptedSections += 1
        do {
            let response: MobileTasksResponse = try await apiClient.request(.tasks(limit: 50))
            tasks = response.tasks
            taskCounts = response.counts
        } catch {
            tasks = []
            taskCounts = MobileTaskCounts(pending: 0, inProgress: 0, blocked: 0, completed: 0)
            markFailure("tasks", error)
        }

        attemptedSections += 1
        do {
            let response: MobileWorkflowsResponse = try await apiClient.request(.workflows(limit: 50))
            workflows = response.workflows
            pendingApprovals = response.pendingApprovals
        } catch {
            workflows = []
            pendingApprovals = 0
            markFailure("workflows", error)
        }

        attemptedSections += 1
        do {
            let response: MobilePresenceResponse = try await apiClient.request(.presence(limit: 50))
            presenceAgents = response.agents
            presenceClaims = response.claims
            presenceWorktrees = response.worktrees
            presenceSummary = response.summary
        } catch {
            presenceAgents = []
            presenceClaims = []
            presenceWorktrees = []
            presenceSummary = MobilePresenceSummary(activeAgents: 0, idleAgents: 0, offlineAgents: 0, totalAgents: 0, claimCount: 0, worktreeCount: 0)
            markFailure("presence", error)
        }

        attemptedSections += 1
        do {
            let response: MobileMemoryStatsResponse = try await apiClient.request(.memoryStats)
            memoryStats = response.stats
        } catch {
            memoryStats = nil
            markFailure("memory_stats", error)
        }

        attemptedSections += 1
        do {
            let response: MobileMemoryItemsResponse = try await apiClient.request(.memoryItems(tier: .working, limit: 50))
            memoryItems = response.items
            memoryTier = response.tier
        } catch {
            memoryItems = []
            memoryTier = .working
            markFailure("memory_items", error)
        }

        attemptedSections += 1
        do {
            let response: MobileStreamResponse = try await apiClient.request(.stream(limit: 50))
            streamEntries = response.entries
        } catch {
            streamEntries = []
            markFailure("stream", error)
        }

        attemptedSections += 1
        do {
            let response: MobileTopologyResponse = try await apiClient.request(.topology)
            topology = response
        } catch {
            topology = nil
            markFailure("topology", error)
        }

        attemptedSections += 1
        do {
            let response: MobileGraphStatsResponse = try await apiClient.request(.graphStats)
            graphStats = response.stats
        } catch {
            graphStats = nil
            markFailure("graph_stats", error)
        }

        attemptedSections += 1
        do {
            let response: MobileGraphEntitiesResponse = try await apiClient.request(.graphEntities(limit: 50))
            graphEntities = response.entities
        } catch {
            graphEntities = []
            markFailure("graph_entities", error)
        }

        graphPath = nil
        if graphEntities.count >= 2 {
            attemptedSections += 1
            do {
                let source = graphEntities[0].id
                let target = graphEntities[1].id
                let response: MobileGraphPathResponse = try await apiClient.request(.graphPath(sourceId: source, targetId: target, maxDepth: 5))
                graphPath = response.path
            } catch {
                markFailure("graph_path", error)
            }
        }

        attemptedSections += 1
        do {
            let response: MobileReasoningChainsResponse = try await apiClient.request(.reasoningChains(limit: 50))
            reasoningChains = response.chains
        } catch {
            reasoningChains = []
            markFailure("reasoning_chains", error)
        }

        if failedSections.count == attemptedSections, let firstError {
            error = firstError
            warningMessage = nil
            return
        }
        if !failedSections.isEmpty {
            warningMessage = "Some sections are unavailable: \(failedSections.joined(separator: ", "))"
        }
    }

    public func loadWorkflowDetail(id: String) async throws -> MobileWorkflowDetailResponse {
        try await apiClient.request(.workflowDetail(id: id))
    }

    public func loadReasoningChainDetail(id: String) async throws -> MobileReasoningChainDetailResponse {
        try await apiClient.request(.reasoningChainDetail(id: id))
    }

    private func toLoomError(_ error: Error) -> LoomAPIError {
        if let loomError = error as? LoomAPIError {
            return loomError
        }
        return .networkError(underlying: error.localizedDescription)
    }
}
