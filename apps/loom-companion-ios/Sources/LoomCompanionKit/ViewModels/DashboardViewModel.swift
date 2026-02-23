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

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
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

    @MainActor
    private func handleSSEEvent(_ event: SSEEvent) async {
        switch event.type {
        case "hud.fleet", "hud.health", "agent.session.start", "agent.session.end":
            await load()
        default:
            break
        }
    }
}
