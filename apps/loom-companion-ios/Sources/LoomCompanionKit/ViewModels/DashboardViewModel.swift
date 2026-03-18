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
        syncWidgets()
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

        // Drive Live Activities from workflow events.
        #if os(iOS)
        if event.type == "hud.workflows" {
            if #available(iOS 16.2, *) {
                handleWorkflowLiveActivities(event)
            }
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
                if lam.activeCount == 0 || completed == 0 {
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
