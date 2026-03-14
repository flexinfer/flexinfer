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
    private var sseTask: Task<Void, Never>?

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
    }

    /// Start listening to SSE events for real-time updates.
    public func startListening(sseClient: SSEClient) {
        sseTask?.cancel()
        sseTask = Task { [weak self] in
            for await event in sseClient.events {
                await self?.handleSSEEvent(event)
            }
        }
    }

    /// Stop listening to SSE events.
    public func stopListening() {
        sseTask?.cancel()
        sseTask = nil
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

    /// SSE event types that trigger widget timeline refresh.
    private static let widgetRefreshEventTypes: Set<String> = [
        "hud.fleet",
        "hud.health",
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

        // Refresh widget timelines on relevant state changes.
        if Self.widgetRefreshEventTypes.contains(event.type) {
            WidgetCenter.shared.reloadAllTimelines()
        }
        #endif

        // Refresh dashboard data for relevant events.
        if Self.refreshEventTypes.contains(event.type) {
            await load()
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
