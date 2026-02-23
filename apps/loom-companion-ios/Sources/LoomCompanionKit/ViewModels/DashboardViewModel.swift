import Foundation

/// ViewModel for the main dashboard screen.
@Observable
public final class DashboardViewModel {
    public var dashboard: DashboardData?
    public var isLoading = false
    public var error: LoomAPIError?

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
    }

    /// Start listening to SSE events for real-time updates.
    public func startListening(sseClient: SSEClient) {
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

        // Refresh dashboard data for relevant events.
        if Self.refreshEventTypes.contains(event.type) {
            await load()
        }
    }
}
