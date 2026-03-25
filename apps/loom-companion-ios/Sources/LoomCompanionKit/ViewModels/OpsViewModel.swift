import Foundation

/// ViewModel powering the read-only mobile parity "Ops" tab.
@Observable
public final class OpsViewModel {
    public var isLoading = false
    public var error: LoomAPIError?
    public var warningMessage: String?
    public var mutationStatusMessage: String?
    public var mutationErrorMessage: String?
    public var isMutatingSession = false

    public var tasks: [MobileTask] = []
    public var taskCounts = MobileTaskCounts(pending: 0, inProgress: 0, blocked: 0, completed: 0)

    public var workflows: [MobileWorkflow] = []
    public var pendingApprovals = 0
    public var workflowsDeprecated = false
    public var workflowsDeprecationMessage: String?

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
    public var controlPlane: MobileControlPlaneResponse?

    public var pipelines: [MobilePipeline] = []
    public var recentPipelines: [MobilePipeline] = []
    public var pipelineSummary: MobilePipelineSummary?
    public var pipelinesAvailable = false

    public var sandboxSummary: MobileSandboxSummary?
    public var isMutatingSandbox = false
    public var sandboxMutationMessage: String?
    public var sandboxMutationError: String?

    @ObservationIgnored
    public let apiClient: any LoomAPIClientProtocol

    @ObservationIgnored
    private var sseRegistrationId: UUID?

    /// SSE event types that trigger a presence/agents refresh.
    private static let refreshEventTypes: Set<String> = [
        "hud.fleet",
        "agent.heartbeat",
        "agent.session.start",
        "agent.session.end",
        "agent.session.reaped",
        "agent.spawn.building",
        "agent.spawn.running",
        "agent.spawn.completed",
        "agent.spawn.failed",
        "agent.spawn.stopped",
        "hud.pipeline",
    ]

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    /// Start listening to SSE events via the broadcaster for real-time agent updates.
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
            await loadPresence()
        }
        if event.type == "hud.pipeline" {
            await loadPipelines()
        }
    }

    /// Refresh just the pipelines section (called on hud.pipeline SSE events).
    public func loadPipelines() async {
        do {
            let response: MobilePipelinesResponse = try await apiClient.request(.pipelines)
            pipelines = response.pipelines
            recentPipelines = response.recentPipelines
            pipelineSummary = response.summary
            pipelinesAvailable = response.available || !response.pipelines.isEmpty || !response.recentPipelines.isEmpty
        } catch {
            // Non-critical — keep existing data on transient failures.
        }
    }

    /// Refresh just the presence/agents section (lightweight, called on SSE events).
    public func loadPresence() async {
        do {
            let response: MobilePresenceResponse = try await apiClient.request(.presence(limit: 50))
            presenceAgents = response.agents
            presenceClaims = response.claims
            presenceWorktrees = response.worktrees
            presenceSummary = response.summary
        } catch {
            // Non-critical — keep existing data on transient failures.
        }
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
            if tasks.isEmpty {
                taskCounts = MobileTaskCounts(pending: 0, inProgress: 0, blocked: 0, completed: 0)
            }
            markFailure("tasks", error)
        }

        attemptedSections += 1
        do {
            let response: MobileWorkflowsResponse = try await apiClient.request(.workflows(limit: 50))
            workflows = response.workflows
            pendingApprovals = response.pendingApprovals
            // Defensive normalization: suppress deprecated flag when nothing to show
            if response.deprecated && response.workflows.isEmpty && response.pendingApprovals == 0 {
                workflowsDeprecated = false
                workflowsDeprecationMessage = nil
            } else {
                workflowsDeprecated = response.deprecated
                workflowsDeprecationMessage = response.deprecationMessage
            }
        } catch {
            workflows = []
            pendingApprovals = 0
            workflowsDeprecated = false
            workflowsDeprecationMessage = nil
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

        attemptedSections += 1
        do {
            let response: MobileControlPlaneResponse = try await apiClient.request(.controlPlane)
            controlPlane = response
        } catch {
            controlPlane = nil
            markFailure("control_plane", error)
        }

        attemptedSections += 1
        do {
            let response: MobileSandboxSummary = try await apiClient.request(.sandbox)
            sandboxSummary = response
        } catch {
            sandboxSummary = nil
            markFailure("sandbox", error)
        }

        attemptedSections += 1
        do {
            let response: MobilePipelinesResponse = try await apiClient.request(.pipelines)
            pipelines = response.pipelines
            recentPipelines = response.recentPipelines
            pipelineSummary = response.summary
            pipelinesAvailable = response.available || !response.pipelines.isEmpty || !response.recentPipelines.isEmpty
        } catch {
            if pipelines.isEmpty {
                pipelinesAvailable = false
            }
            markFailure("pipelines", error)
        }

        if failedSections.count == attemptedSections, let firstError {
            error = firstError
            warningMessage = nil
            return
        }
        // Only surface warnings for core section failures; optional/advanced
        // sections (reasoning, graph, control plane, sandbox, topology) failing
        // is expected when the server doesn't expose those endpoints yet.
        let coreSections: Set<String> = ["tasks", "presence", "stream", "pipelines"]
        let coreFailures = failedSections.filter { coreSections.contains($0) }
        if !coreFailures.isEmpty {
            warningMessage = "Some sections are unavailable: \(coreFailures.joined(separator: ", "))"
        }
    }

    public func loadWorkflowDetail(id: String) async throws -> MobileWorkflowDetailResponse {
        try await apiClient.request(.workflowDetail(id: id))
    }

    public func loadReasoningChainDetail(id: String) async throws -> MobileReasoningChainDetailResponse {
        try await apiClient.request(.reasoningChainDetail(id: id))
    }

    /// Start a session using the mobile mutation endpoint.
    public func createSession(agentID: String, namespace: String?, description: String?, autoRecall: Bool) async {
        let trimmedAgentID = agentID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedAgentID.isEmpty else {
            mutationErrorMessage = "Agent ID is required"
            return
        }

        isMutatingSession = true
        mutationErrorMessage = nil
        defer { isMutatingSession = false }

        do {
            let response: SessionCreateResponse = try await apiClient.request(
                .createSession(
                    agentId: trimmedAgentID,
                    namespace: normalizedOptional(namespace),
                    description: normalizedOptional(description),
                    autoRecall: autoRecall
                )
            )
            mutationStatusMessage = response.alreadyExisted
                ? "Session already exists: \(response.sessionId)"
                : "Session started: \(response.sessionId)"
        } catch {
            mutationErrorMessage = toLoomError(error).description
        }
    }

    /// End a session using the mobile mutation endpoint.
    public func endSession(sessionID: String, summarize: Bool) async {
        let trimmedSessionID = sessionID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedSessionID.isEmpty else {
            mutationErrorMessage = "Session ID is required"
            return
        }

        isMutatingSession = true
        mutationErrorMessage = nil
        defer { isMutatingSession = false }

        do {
            let response: SessionEndResponse = try await apiClient.request(.endSession(id: trimmedSessionID, summarize: summarize))
            mutationStatusMessage = response.ended
                ? "Session ended: \(response.sessionId)"
                : "No active session ended for \(response.sessionId)"
        } catch {
            mutationErrorMessage = toLoomError(error).description
        }
    }

    public func startSandbox(project: String, agentID: String?) async {
        let trimmedProject = project.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedProject.isEmpty else {
            sandboxMutationError = "Project is required"
            return
        }

        isMutatingSandbox = true
        sandboxMutationError = nil
        defer { isMutatingSandbox = false }

        do {
            let response: MobileSandboxStartResponse = try await apiClient.request(
                .sandboxStart(project: trimmedProject, agentId: normalizedOptional(agentID))
            )
            sandboxMutationMessage = response.started
                ? "Sandbox started: \(response.project)"
                : "Sandbox build queued: \(response.project)"
            await refreshSandboxSummaryAfterMutation()
        } catch {
            sandboxMutationError = toLoomError(error).description
        }
    }

    public func stopSandbox(project: String) async {
        let trimmedProject = project.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedProject.isEmpty else {
            sandboxMutationError = "Project is required"
            return
        }

        isMutatingSandbox = true
        sandboxMutationError = nil
        defer { isMutatingSandbox = false }

        do {
            let response: MobileSandboxStopResponse = try await apiClient.request(
                .sandboxStop(project: trimmedProject)
            )
            sandboxMutationMessage = response.stopped
                ? "Sandbox stopped: \(response.project)"
                : "Sandbox stop requested: \(response.project)"
            await refreshSandboxSummaryAfterMutation()
        } catch {
            sandboxMutationError = toLoomError(error).description
        }
    }

    public func clearMutationMessages() {
        mutationStatusMessage = nil
        mutationErrorMessage = nil
        sandboxMutationMessage = nil
        sandboxMutationError = nil
    }

    private func normalizedOptional(_ value: String?) -> String? {
        guard let value else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    private func refreshSandboxSummaryAfterMutation() async {
        do {
            let latest: MobileSandboxSummary = try await apiClient.request(.sandbox)
            sandboxSummary = latest
        } catch {
            // Keep mutation success visible even if follow-up refresh fails.
        }
    }

    private func toLoomError(_ error: Error) -> LoomAPIError {
        if let loomError = error as? LoomAPIError {
            return loomError
        }
        return .networkError(underlying: error.localizedDescription)
    }
}
