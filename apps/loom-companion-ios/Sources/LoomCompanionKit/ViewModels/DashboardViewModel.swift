import Foundation
#if os(iOS)
import WidgetKit
#endif

/// ViewModel for the main dashboard screen.
@Observable
public final class DashboardViewModel {
    public var dashboard: DashboardData?
    public var isLoading = false
    public var error: LoomAPIError?
    public var taskCounts: MobileTaskCounts?

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    @ObservationIgnored
    private var sseRegistrationId: UUID?

    @ObservationIgnored
    public var alertsViewModel: AlertsViewModel?

    public init(apiClient: any LoomAPIClientProtocol, alertsViewModel: AlertsViewModel? = nil) {
        self.apiClient = apiClient
        self.alertsViewModel = alertsViewModel
    }

    /// Fetch dashboard data from REST API.
    public func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            dashboard = try await apiClient.request(.dashboard)
            error = nil
        } catch let err as LoomAPIError {
            error = err
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
        }

        // Fetch task counts (best-effort, non-blocking)
        do {
            let response: MobileTasksResponse = try await apiClient.request(.tasks(limit: 1))
            taskCounts = response.counts
        } catch {
            // Non-critical — dashboard works without task counts
        }

        #if os(iOS)
        await MainActor.run { syncWidgets() }
        #endif
    }

    /// Start listening to SSE events via the broadcaster for real-time updates.
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

    /// SSE event types that trigger a dashboard data refresh.
    private static let refreshEventTypes: Set<String> = [
        "hud.fleet",
        "hud.health",
        "agent.session.start",
        "agent.session.end",
        "agent.session.reaped",
        "agent.heartbeat",
        "hud.handoff.created",
        "hud.workflows",
        "hud.pipeline",
    ]

    /// SSE event types that are notification-worthy (forwarded to AlertsViewModel).
    private static let notificationEventTypes: Set<String> = [
        "hud.health",
        "agent.session.start",
        "agent.session.end",
        "agent.session.reaped",
        "agent.nudge.created",
        "hud.workflow.approve",
        "hud.workflow.reject",
        "hud.handoff.created",
        "coordinator.plan.complete",
    ]

    @MainActor
    private func handleSSEEvent(_ event: SSEEvent) async {
        // Forward notification-worthy events to the alerts VM.
        if Self.notificationEventTypes.contains(event.type) {
            alertsViewModel?.handleSSEEvent(event)
        }

        #if os(iOS)
        if #available(iOS 16.2, *) {
            // Drive Live Activities from workflow events.
            if event.type == "hud.workflows" {
                handleWorkflowLiveActivities(event)
            }

            // Drive Live Activities from session/heartbeat events.
            switch event.type {
            case "agent.session.start":
                handleSessionStartLiveActivity(event)
            case "agent.heartbeat":
                handleHeartbeatLiveActivity(event)
            case "agent.session.end", "agent.session.reaped":
                handleSessionEndLiveActivity(event)
            case "agent.session.stats.updated":
                handleSessionStatsLiveActivity(event)
            case "hud.pipeline":
                handlePipelineLiveActivities(event)
            default:
                break
            }
        }

        // Trigger widget refresh on session lifecycle events.
        if event.type == "agent.session.end" || event.type == "hud.fleet" {
            WidgetCenter.shared.reloadAllTimelines()
        }
        #endif

        // Refresh dashboard data for relevant events.
        if Self.refreshEventTypes.contains(event.type) {
            await load()
        }
    }

    @MainActor
    private func syncWidgets() {
        guard let dashboard else { return }

        let counts = taskCounts ?? MobileTaskCounts(
            pending: 0,
            inProgress: 0,
            blocked: 0,
            completed: 0
        )

        SharedDataStore.save(
            WidgetData(
                fleet: FleetWidgetData(
                    daemonRunning: dashboard.daemonRunning,
                    serverCount: dashboard.serverCount,
                    sessionCount: dashboard.activeSessions,
                    activeAgents: dashboard.activeAgents,
                    idleAgents: dashboard.idleAgents,
                    offlineAgents: dashboard.offlineAgents,
                    healthyServers: dashboard.health.healthyServers,
                    degradedServers: dashboard.health.degradedServers,
                    downServers: dashboard.health.downServers
                ),
                tasks: TaskWidgetData(
                    pending: counts.pending,
                    inProgress: counts.inProgress,
                    blocked: counts.blocked,
                    completed: counts.completed,
                    recentTitles: recentTaskTitles(from: dashboard.recentTimeline)
                ),
                sessions: SessionWidgetData(
                    activeCount: dashboard.activeSessions,
                    topSessions: recentSessions(from: dashboard.recentTimeline)
                )
            )
        )

        WidgetCenter.shared.reloadAllTimelines()
    }

    private func recentTaskTitles(from timeline: [TimelineEntry]) -> [String] {
        timeline.compactMap { entry in
            guard
                entry.eventType.contains("task"),
                let title = entry.data?["title"]?.stringValue,
                !title.isEmpty
            else {
                return nil
            }
            return title
        }
    }

    private func recentSessions(from timeline: [TimelineEntry]) -> [SessionWidgetEntry] {
        timeline.compactMap { entry in
            guard entry.eventType.contains("session") else { return nil }

            return SessionWidgetEntry(
                id: entry.id,
                namespace: entry.data?["namespace"]?.stringValue ?? entry.eventType,
                agentId: entry.agentId ?? entry.data?["agent_id"]?.stringValue ?? "unknown",
                startedAt: entry.timestamp
            )
        }
    }

    #if os(iOS)
    // MARK: - Session Live Activity Handlers

    @available(iOS 16.2, *)
    @MainActor
    private func handleSessionStartLiveActivity(_ event: SSEEvent) {
        guard let data = event.data.data(using: .utf8) else { return }

        struct SessionStartPayload: Decodable {
            let session_id: String?
            let agent_id: String?
            let agent_type: String?
            let namespace: String?
            let description: String?
            let branch: String?
        }

        guard let payload = try? JSONDecoder().decode(SessionStartPayload.self, from: data),
              let sessionId = payload.session_id,
              let agentId = payload.agent_id else { return }

        let agentType = payload.agent_type ?? inferAgentType(from: agentId)
        let namespace = payload.namespace ?? agentId

        let lam = LiveActivityManager.shared
        lam.startSessionActivity(
            sessionId: sessionId,
            agentId: agentId,
            agentType: agentType,
            namespace: namespace,
            branch: payload.branch ?? ""
        )
    }

    @available(iOS 16.2, *)
    @MainActor
    private func handleHeartbeatLiveActivity(_ event: SSEEvent) {
        guard let data = event.data.data(using: .utf8) else { return }

        struct HeartbeatPayload: Decodable {
            let agent_id: String?
            let status: String?
            let current_task: String?
            let branch: String?
        }

        guard let payload = try? JSONDecoder().decode(HeartbeatPayload.self, from: data),
              let agentId = payload.agent_id else { return }

        let lam = LiveActivityManager.shared
        guard let sessionId = lam.sessionId(forAgent: agentId) else { return }

        lam.updateSessionActivity(
            sessionId: sessionId,
            status: payload.status ?? "active",
            currentTask: payload.current_task,
            branch: payload.branch
        )
    }

    @available(iOS 16.2, *)
    @MainActor
    private func handleSessionEndLiveActivity(_ event: SSEEvent) {
        guard let data = event.data.data(using: .utf8) else { return }

        struct SessionEndPayload: Decodable {
            let session_id: String?
            let agent_id: String?
        }

        guard let payload = try? JSONDecoder().decode(SessionEndPayload.self, from: data) else { return }

        let lam = LiveActivityManager.shared
        if let sessionId = payload.session_id {
            lam.endSessionActivity(sessionId: sessionId)
        } else if let agentId = payload.agent_id {
            lam.endSessionActivityByAgent(agentId: agentId)
        }
    }

    @available(iOS 16.2, *)
    @MainActor
    private func handleSessionStatsLiveActivity(_ event: SSEEvent) {
        guard let data = event.data.data(using: .utf8) else { return }

        struct SessionStatsPayload: Decodable {
            let session_id: String?
            let agent_id: String?
            let total_tokens: Int?
            let entry_count: Int?
        }

        guard let payload = try? JSONDecoder().decode(SessionStatsPayload.self, from: data) else { return }

        let lam = LiveActivityManager.shared
        let sessionId: String?
        if let sid = payload.session_id {
            sessionId = sid
        } else if let aid = payload.agent_id {
            sessionId = lam.sessionId(forAgent: aid)
        } else {
            sessionId = nil
        }

        guard let sid = sessionId else { return }
        lam.updateSessionActivity(
            sessionId: sid,
            tokenCount: payload.total_tokens,
            entryCount: payload.entry_count
        )
    }

    /// Infer agent type from agent ID string.
    private func inferAgentType(from agentId: String) -> String {
        let id = agentId.lowercased()
        if id.contains("claude") { return "claude-code" }
        if id.contains("gemini") { return "gemini" }
        if id.contains("codex") { return "codex" }
        if id.contains("kilo") { return "kilocode" }
        if id.contains("antigravity") { return "antigravity" }
        return "unknown"
    }

    // MARK: - Pipeline Live Activity Handlers

    @available(iOS 16.2, *)
    @MainActor
    private func handlePipelineLiveActivities(_ event: SSEEvent) {
        guard let data = event.data.data(using: .utf8) else { return }

        struct PipelinePayload: Decodable {
            let pipelines: [PipelineItem]?
        }
        struct PipelineItem: Decodable {
            let id: Int
            let project: String
            let ref: String
            let status: String
            let current_stage: String?
            let completed_stages: Int?
            let total_stages: Int?
            let failed_job_count: Int?
        }

        guard let payload = try? JSONDecoder().decode(PipelinePayload.self, from: data),
              let pipelines = payload.pipelines else { return }

        let lam = LiveActivityManager.shared
        for pipeline in pipelines {
            let total = pipeline.total_stages ?? 1
            let completed = pipeline.completed_stages ?? 0
            let stage = pipeline.current_stage ?? pipeline.status

            switch pipeline.status {
            case "running", "pending":
                if lam.activePipelineCount == 0 || completed == 0 {
                    lam.startPipelineActivity(
                        pipelineId: pipeline.id,
                        project: pipeline.project,
                        ref: pipeline.ref,
                        currentStage: stage,
                        totalStages: total
                    )
                } else {
                    lam.updatePipelineActivity(
                        pipelineId: pipeline.id,
                        status: pipeline.status,
                        currentStage: stage,
                        completedStages: completed,
                        totalStages: total,
                        failedJobCount: pipeline.failed_job_count ?? 0
                    )
                }
            case "success", "failed", "canceled":
                lam.endPipelineActivity(
                    pipelineId: pipeline.id,
                    finalStatus: pipeline.status
                )
            default:
                break
            }
        }
    }

    // MARK: - Workflow Live Activity Handlers

    /// Parse workflow SSE events and drive Live Activity start/update/end.
    @available(iOS 16.2, *)
    @MainActor
    private func handleWorkflowLiveActivities(_ event: SSEEvent) {
        guard let data = event.data.data(using: .utf8) else { return }

        struct WorkflowPayload: Decodable {
            let workflows: [WorkflowItem]?
        }
        struct WorkflowItem: Decodable {
            let workflow_id: String
            let name: String?
            let status: String
            let current_step: String?
            let completed_steps: Int?
            let total_steps: Int?
            let progress: Double?
        }

        guard let payload = try? JSONDecoder().decode(WorkflowPayload.self, from: data),
              let workflows = payload.workflows else { return }

        let lam = LiveActivityManager.shared
        for wf in workflows {
            let total = wf.total_steps ?? 1
            let completed = wf.completed_steps ?? 0
            let step = wf.current_step ?? wf.status

            switch wf.status {
            case "running", "in_progress":
                if lam.activeWorkflowCount == 0 || completed == 0 {
                    lam.startWorkflowActivity(
                        workflowId: wf.workflow_id,
                        name: wf.name ?? wf.workflow_id,
                        agentId: "",
                        initialStep: step,
                        totalSteps: total
                    )
                } else {
                    lam.updateWorkflowActivity(
                        workflowId: wf.workflow_id,
                        stepName: step,
                        stepIndex: completed,
                        totalSteps: total,
                        status: wf.status,
                        elapsedSeconds: 0
                    )
                }
            case "completed", "failed", "cancelled":
                lam.endWorkflowActivity(
                    workflowId: wf.workflow_id,
                    finalStatus: wf.status
                )
            default:
                break
            }
        }
    }
    #endif
}
