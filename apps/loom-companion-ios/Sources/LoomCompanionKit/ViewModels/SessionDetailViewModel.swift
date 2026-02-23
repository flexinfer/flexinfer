import Foundation

/// ViewModel for the session detail screen.
@Observable
public final class SessionDetailViewModel {
    public var session: SessionInfo?
    public var events: [TimelineEntry] = []
    public var isLoading = false
    public var error: LoomAPIError?

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    /// Fetch session detail and events concurrently.
    public func load(sessionId: String) async {
        isLoading = true
        defer { isLoading = false }

        do {
            async let detailResult: SessionDetailResponse = apiClient.request(.sessionDetail(id: sessionId))
            async let eventsResult: SessionEventsResponse = apiClient.request(.sessionEvents(id: sessionId, limit: 100))

            let (detail, sessionEvents) = try await (detailResult, eventsResult)
            session = detail.session
            events = sessionEvents.events
            error = nil
        } catch let err as LoomAPIError {
            error = err
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
        }
    }
}
